<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";

import LiveMap from "./components/LiveMap.vue";
import type { DriverLiveLocation, LoginResult, NearbyDriver, Order, SocketEvent, Trip, User } from "./types";

const storageKey = "passenger-lab-session";

const authMode = ref<"login" | "register">("login");
const savingAuth = ref(false);
const loadingOrders = ref(false);
const loadingNearby = ref(false);
const savingOrder = ref(false);
const locating = ref(false);
const loadingTrip = ref(false);
const socketState = ref<"idle" | "connecting" | "open" | "closed">("idle");
const mapReady = ref(false);
const mapPickMode = ref<"pickup" | "destination">("pickup");
const draftMode = ref(true);
const message = ref("欢迎来到乘客测试台。先登录一个乘客账号，我们就能把下单和派单整条链路跑起来。");
const orders = ref<Order[]>([]);
const nearbyDrivers = ref<NearbyDriver[]>([]);
const selectedOrderId = ref("");
const selectedTrip = ref<Trip | null>(null);
const socketEvents = ref<Array<{ id: number; text: string }>>([]);
const liveDriverLocations = ref<Record<string, DriverLiveLocation>>({});
const ws = ref<WebSocket | null>(null);
let pollTimer: number | null = null;
let eventSeq = 0;

const session = reactive<{
  token: string;
  user: User | null;
}>({
  token: "",
  user: null,
});

const authForm = reactive({
  phone: "13800138000",
  password: "pass123456",
  display_name: "Passenger Lab",
});

const nearbyForm = reactive({
  lat: 31.2304,
  lng: 121.4737,
  radius_m: 3000,
  limit: 8,
});

const orderForm = reactive({
  pickup_lat: 31.2304,
  pickup_lng: 121.4737,
  pickup_address: "人民广场",
  destination_lat: 31.2204,
  destination_lng: 121.4637,
  destination_address: "静安寺",
  estimated_price: 38,
});

const selectedOrder = computed(() => {
  if (draftMode.value) {
    return null;
  }
  return orders.value.find((item) => item.id === selectedOrderId.value) ?? null;
});
const isAuthenticated = computed(() => Boolean(session.token && session.user));
const selectedOrderCanCancel = computed(() => selectedOrder.value?.status === "pending_dispatch");
const selectedOrderCanPay = computed(() => {
  return selectedOrder.value?.status === "to_be_paid" || selectedOrder.value?.status === "completed";
});
const mergedDrivers = computed(() =>
  nearbyDrivers.value.map((driver) => {
    const live = liveDriverLocations.value[driver.driver_id];
    if (!live) {
      return driver;
    }
    return {
      ...driver,
      location: {
        ...driver.location,
        lat: live.lat,
        lng: live.lng,
      },
    };
  }),
);
const mapPickupLat = computed(() => selectedOrder.value?.pickup_lat ?? orderForm.pickup_lat);
const mapPickupLng = computed(() => selectedOrder.value?.pickup_lng ?? orderForm.pickup_lng);
const mapDestinationLat = computed(() => selectedOrder.value?.destination_lat ?? orderForm.destination_lat);
const mapDestinationLng = computed(() => selectedOrder.value?.destination_lng ?? orderForm.destination_lng);
const displayDrivers = computed(() => {
  const items = [...mergedDrivers.value];
  const seen = new Set(items.map((driver) => driver.driver_id));

  for (const [driverID, live] of Object.entries(liveDriverLocations.value)) {
    if (seen.has(driverID)) {
      continue;
    }
    items.push({
      driver_id: driverID,
      status: "live",
      distance_m: 0,
      location: {
        lat: live.lat,
        lng: live.lng,
        timestamp: live.timestamp ?? new Date().toISOString(),
      },
    });
    seen.add(driverID);
  }

  const current = selectedOrder.value;
  if (!current?.driver_id) {
    return items;
  }

  const exists = seen.has(current.driver_id);
  const live = liveDriverLocations.value[current.driver_id];
  if (exists || !live) {
    return items;
  }

  items.push({
    driver_id: current.driver_id,
    status: current.status,
    distance_m: 0,
    location: {
      lat: live.lat,
      lng: live.lng,
      timestamp: new Date().toISOString(),
    },
  });
  return items;
});

onMounted(() => {
  restoreSession();
  if (isAuthenticated.value) {
    void bootstrapAuthedView();
  }
});

onBeforeUnmount(() => {
  stopPolling();
  disconnectSocket();
});

watch(selectedOrderId, async (next) => {
  if (!next) {
    selectedTrip.value = null;
    return;
  }
  await loadTrip(next);
});

async function bootstrapAuthedView() {
  await Promise.all([locatePassenger(true), loadOrders(), loadNearby()]);
  connectSocket();
  startPolling();
}

async function submitAuth() {
  savingAuth.value = true;
  try {
    if (authMode.value === "register") {
      await api("/api/v1/auth/register", {
        method: "POST",
        body: {
          phone: authForm.phone,
          password: authForm.password,
          role: "passenger",
          display_name: authForm.display_name,
        },
      });
      message.value = "注册成功，已经准备好登录。";
      authMode.value = "login";
    }

    const result = await api<{ item: LoginResult }>("/api/v1/auth/login", {
      method: "POST",
      body: {
        phone: authForm.phone,
        password: authForm.password,
      },
    });

    session.token = result.item.token;
    session.user = result.item.user;
    persistSession();
    message.value = `已登录 ${result.item.user.phone}，现在可以查询附近司机并创建订单。`;
    await bootstrapAuthedView();
  } catch (error) {
    message.value = asErrorMessage(error);
  } finally {
    savingAuth.value = false;
  }
}

async function locatePassenger(silent = false) {
  if (!("geolocation" in navigator)) {
    if (!silent) {
      message.value = "当前浏览器不支持定位。";
    }
    return;
  }

  locating.value = true;
  try {
    const position = await new Promise<GeolocationPosition>((resolve, reject) => {
      navigator.geolocation.getCurrentPosition(resolve, reject, {
        enableHighAccuracy: true,
        timeout: 12000,
        maximumAge: 10000,
      });
    });

    const lat = Number(position.coords.latitude.toFixed(6));
    const lng = Number(position.coords.longitude.toFixed(6));

    nearbyForm.lat = lat;
    nearbyForm.lng = lng;
    orderForm.pickup_lat = lat;
    orderForm.pickup_lng = lng;
    message.value = `已定位到当前位置 (${lat.toFixed(4)}, ${lng.toFixed(4)})。`;
  } catch (error) {
    if (!silent) {
      message.value = `定位失败：${asErrorMessage(error)}`;
    }
  } finally {
    locating.value = false;
  }
}

async function loadNearby() {
  if (!isAuthenticated.value) return;
  loadingNearby.value = true;
  try {
    const query = new URLSearchParams({
      lat: String(nearbyForm.lat),
      lng: String(nearbyForm.lng),
      radius_m: String(nearbyForm.radius_m),
      limit: String(nearbyForm.limit),
    });
    const result = await api<{ items: NearbyDriver[] }>(`/api/v1/drivers/nearby?${query.toString()}`);
    nearbyDrivers.value = result.items;
    message.value = `已拉取 ${result.items.length} 位附近司机。`;
  } catch (error) {
    message.value = asErrorMessage(error);
  } finally {
    loadingNearby.value = false;
  }
}

async function createOrder() {
  if (!isAuthenticated.value) return;
  savingOrder.value = true;
  try {
    const result = await api<{ item: Order }>("/api/v1/orders", {
      method: "POST",
      body: {
        pickup_lat: orderForm.pickup_lat,
        pickup_lng: orderForm.pickup_lng,
        pickup_address: orderForm.pickup_address,
        destination_lat: orderForm.destination_lat,
        destination_lng: orderForm.destination_lng,
        destination_address: orderForm.destination_address,
        estimated_price: Number(orderForm.estimated_price),
      },
    });
    orders.value = [result.item, ...orders.value.filter((item) => item.id !== result.item.id)];
    draftMode.value = false;
    selectedOrderId.value = result.item.id;
    message.value = `订单 ${result.item.id} 已创建，正在等待派单。`;
  } catch (error) {
    message.value = asErrorMessage(error);
  } finally {
    savingOrder.value = false;
  }
}

async function loadOrders() {
  if (!isAuthenticated.value) return;
  loadingOrders.value = true;
  try {
    const result = await api<{ items: Order[] }>("/api/v1/orders");
    orders.value = result.items;
    if (!draftMode.value && !selectedOrderId.value && result.items.length > 0) {
      selectedOrderId.value = result.items[0].id;
    } else if (selectedOrderId.value && !result.items.some((item) => item.id === selectedOrderId.value)) {
      selectedOrderId.value = draftMode.value ? "" : result.items[0]?.id ?? "";
    }
  } catch (error) {
    message.value = asErrorMessage(error);
  } finally {
    loadingOrders.value = false;
  }
}

async function updateSelectedOrder(status: "cancelled" | "paid") {
  if (!selectedOrder.value) return;
  try {
    const result = await api<{ item: Order }>(`/api/v1/orders/${selectedOrder.value.id}/status`, {
      method: "POST",
      body: { status },
    });
    replaceOrder(result.item);
    message.value = `订单 ${result.item.id} 已更新为 ${result.item.status}。`;
  } catch (error) {
    message.value = asErrorMessage(error);
  }
}

async function loadTrip(orderId: string) {
  if (!orderId || !isAuthenticated.value) return;
  loadingTrip.value = true;
  try {
    const result = await api<{ item: Trip }>(`/api/v1/orders/${orderId}/trip`);
    selectedTrip.value = result.item;
  } catch {
    selectedTrip.value = null;
  } finally {
    loadingTrip.value = false;
  }
}

function startDraftOrder() {
  if (draftMode.value && !selectedOrderId.value) {
    return;
  }
  draftMode.value = true;
  selectedOrderId.value = "";
  selectedTrip.value = null;
  message.value = "已切换到新订单草稿，当前可以重新设置上车点和目的地。";
}

function connectSocket() {
  if (!session.token) return;
  disconnectSocket();
  socketState.value = "connecting";
  const protocol = window.location.protocol === "https:" ? "wss" : "ws";
  const url = `${protocol}://${window.location.host}/ws/location?token=${encodeURIComponent(session.token)}`;
  const socket = new WebSocket(url);
  ws.value = socket;

  socket.onopen = () => {
    socketState.value = "open";
    pushEvent("WebSocket 已连接，正在接收实时位置和派单广播。");
  };
  socket.onclose = () => {
    socketState.value = "closed";
  };
  socket.onerror = () => {
    socketState.value = "closed";
    pushEvent("WebSocket 连接遇到错误。");
  };
  socket.onmessage = (event) => {
    try {
      const payload = JSON.parse(event.data) as SocketEvent;
      handleSocketEvent(payload);
    } catch {
      pushEvent(`收到非 JSON 消息: ${event.data}`);
    }
  };
}

function disconnectSocket() {
  if (ws.value) {
    ws.value.close();
    ws.value = null;
  }
}

function startPolling() {
  stopPolling();
  pollTimer = window.setInterval(() => {
    void loadOrders();
  }, 5000);
}

function stopPolling() {
  if (pollTimer !== null) {
    window.clearInterval(pollTimer);
    pollTimer = null;
  }
}

function handleSocketEvent(event: SocketEvent) {
  pushEvent(formatSocketEvent(event));

  if (event.type === "driver.location.updated" && isRecord(event.data)) {
    const driverId = String(event.data.driver_id ?? "");
    if (driverId) {
      liveDriverLocations.value = {
        ...liveDriverLocations.value,
        [driverId]: {
          lat: Number(event.data.lat ?? 0),
          lng: Number(event.data.lng ?? 0),
          heading: Number(event.data.heading ?? 0),
          timestamp: event.data.timestamp ? String(event.data.timestamp) : undefined,
          order_id: event.data.order_id ? String(event.data.order_id) : undefined,
        },
      };
    }
  }

  if (event.type?.startsWith("dispatch.") || event.type?.startsWith("driver.location")) {
    void loadOrders();
    void loadNearby();
  }
}

function pushEvent(text: string) {
  socketEvents.value = [{ id: ++eventSeq, text }, ...socketEvents.value].slice(0, 20);
}

function restoreSession() {
  const raw = localStorage.getItem(storageKey);
  if (!raw) return;
  try {
    const parsed = JSON.parse(raw) as { token: string; user: User };
    session.token = parsed.token;
    session.user = parsed.user;
  } catch {
    localStorage.removeItem(storageKey);
  }
}

function persistSession() {
  if (!session.token || !session.user) return;
  localStorage.setItem(storageKey, JSON.stringify({ token: session.token, user: session.user }));
}

function logout() {
  draftMode.value = true;
  session.token = "";
  session.user = null;
  orders.value = [];
  nearbyDrivers.value = [];
  selectedOrderId.value = "";
  selectedTrip.value = null;
  liveDriverLocations.value = {};
  localStorage.removeItem(storageKey);
  disconnectSocket();
  stopPolling();
  message.value = "已退出登录。";
}

function replaceOrder(order: Order) {
  orders.value = [order, ...orders.value.filter((item) => item.id !== order.id)];
  draftMode.value = false;
  selectedOrderId.value = order.id;
}

function selectOrder(orderID: string) {
  draftMode.value = false;
  selectedOrderId.value = orderID;
}

function onMapPicked(payload: { mode: "pickup" | "destination"; lat: number; lng: number }) {
  startDraftOrder();

  if (payload.mode === "pickup") {
    orderForm.pickup_lat = Number(payload.lat.toFixed(6));
    orderForm.pickup_lng = Number(payload.lng.toFixed(6));
    nearbyForm.lat = orderForm.pickup_lat;
    nearbyForm.lng = orderForm.pickup_lng;
    message.value = `已通过地图选择上车点 (${orderForm.pickup_lat.toFixed(4)}, ${orderForm.pickup_lng.toFixed(4)})。`;
    return;
  }

  orderForm.destination_lat = Number(payload.lat.toFixed(6));
  orderForm.destination_lng = Number(payload.lng.toFixed(6));
  message.value = `已通过地图选择目的地 (${orderForm.destination_lat.toFixed(4)}, ${orderForm.destination_lng.toFixed(4)})。`;
}

function formatMoney(value?: number) {
  return typeof value === "number" ? `¥${value.toFixed(2)}` : "-";
}

function formatSocketEvent(event: SocketEvent) {
  if (event.type === "dispatch.created") {
    return `已创建派单候选，数量 ${event.count ?? 0}`;
  }
  if (event.type === "dispatch.accepted") {
    return `司机 ${event.driver_id ?? "-"} 已接单 ${event.order_id ?? "-"}`;
  }
  if (event.type === "dispatch.closed") {
    return `订单 ${event.order_id ?? "-"} 的待派单已关闭，状态 ${event.status ?? "-"}`;
  }
  if (event.type === "driver.location.updated" && isRecord(event.data)) {
    return `司机 ${event.data.driver_id ?? "-"} 位置更新到 (${Number(event.data.lat ?? 0).toFixed(4)}, ${Number(event.data.lng ?? 0).toFixed(4)})`;
  }
  if (event.type === "driver.location.batch.updated") {
    return `收到一批位置更新，共 ${event.count ?? 0} 条`;
  }
  if (event.type === "driver.heartbeat.received" && isRecord(event.data)) {
    return `司机 ${event.data.driver_id ?? "-"} 心跳到达`;
  }
  return JSON.stringify(event);
}

async function api<T>(path: string, options: { method?: string; body?: unknown } = {}): Promise<T> {
  const response = await fetch(path, {
    method: options.method ?? "GET",
    headers: {
      "Content-Type": "application/json",
      ...(session.token ? { Authorization: `Bearer ${session.token}` } : {}),
    },
    body: options.body ? JSON.stringify(options.body) : undefined,
  });

  const raw = await response.text();
  const data = raw ? JSON.parse(raw) : {};
  if (!response.ok) {
    throw new Error(data.error ?? `请求失败: ${response.status}`);
  }
  return data as T;
}

function asErrorMessage(error: unknown) {
  if (error instanceof GeolocationPositionError) {
    return error.message;
  }
  return error instanceof Error ? error.message : "发生未知错误";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object";
}
</script>

<template>
  <div class="shell">
    <header class="hero">
      <div>
        <p class="eyebrow">Passenger Frontend Lab</p>
        <h1>高德地图乘客端联调台</h1>
        <p class="hero-copy">
          现在这版已经接入高德地图底图和浏览器定位。我们可以直接在地图上选起终点、刷新附近司机、创建订单，
          再观察自动派单和司机位置实时更新。
        </p>
      </div>

      <div class="status-cluster">
        <div class="status-pill" :data-state="socketState">
          <span class="dot"></span>
          WebSocket: {{ socketState }}
        </div>
        <div class="status-pill" :data-state="mapReady ? 'open' : 'closed'">
          <span class="dot"></span>
          Map: {{ mapReady ? "ready" : "waiting" }}
        </div>
        <div v-if="session.user" class="account-pill">
          <strong>{{ session.user.display_name || session.user.phone }}</strong>
          <span>{{ session.user.role }}</span>
        </div>
      </div>
    </header>

    <main class="grid">
      <section class="panel auth-panel">
        <div class="panel-head">
          <h2>账号</h2>
          <div class="toggle">
            <button :class="{ active: authMode === 'login' }" @click="authMode = 'login'">登录</button>
            <button :class="{ active: authMode === 'register' }" @click="authMode = 'register'">注册</button>
          </div>
        </div>

        <template v-if="!isAuthenticated">
          <label>
            手机号
            <input v-model="authForm.phone" placeholder="13800138000" />
          </label>
          <label v-if="authMode === 'register'">
            昵称
            <input v-model="authForm.display_name" placeholder="Passenger Lab" />
          </label>
          <label>
            密码
            <input v-model="authForm.password" type="password" placeholder="pass123456" />
          </label>
          <button class="primary" :disabled="savingAuth" @click="submitAuth">
            {{ savingAuth ? "提交中..." : authMode === "login" ? "登录并进入测试台" : "注册并登录" }}
          </button>
        </template>

        <template v-else>
          <div class="session-card">
            <div>
              <strong>{{ session.user?.display_name || session.user?.phone }}</strong>
              <p>{{ session.user?.phone }}</p>
            </div>
            <button class="ghost" @click="logout">退出</button>
          </div>
          <div class="stack-buttons">
            <button class="secondary" :disabled="locating" @click="locatePassenger()">
              {{ locating ? "定位中..." : "定位到当前位置" }}
            </button>
            <button class="secondary" @click="loadOrders">刷新订单</button>
            <button class="secondary" @click="loadNearby">刷新附近司机</button>
          </div>
        </template>

        <div class="message-box">{{ message }}</div>
      </section>

      <section class="panel map-panel">
        <div class="panel-head">
          <h2>高德地图</h2>
          <div class="actions">
            <button class="ghost" :class="{ active: mapPickMode === 'pickup' }" @click="mapPickMode = 'pickup'">点地图设上车点</button>
            <button class="ghost" :class="{ active: mapPickMode === 'destination' }" @click="mapPickMode = 'destination'">点地图设目的地</button>
          </div>
        </div>

        <LiveMap
          :pickup-lat="mapPickupLat"
          :pickup-lng="mapPickupLng"
          :destination-lat="mapDestinationLat"
          :destination-lng="mapDestinationLng"
          :drivers="displayDrivers"
          :live-driver-locations="liveDriverLocations"
          :current-order="selectedOrder"
          :pick-mode="mapPickMode"
          @pick-location="onMapPicked"
          @ready="mapReady = true"
          @error="message = $event"
        />

        <div class="driver-strip">
          <article v-for="driver in displayDrivers" :key="driver.driver_id" class="driver-chip">
            <strong>{{ driver.driver_id }}</strong>
            <span>{{ driver.status }}</span>
            <em>{{ Math.round(driver.distance_m) }}m</em>
          </article>
          <p v-if="displayDrivers.length === 0" class="empty-hint">还没有附近司机。启动司机模拟器后这里就会热起来。</p>
        </div>
      </section>

      <section class="panel order-panel">
        <div class="panel-head">
          <h2>创建订单</h2>
          <div class="actions">
            <button class="ghost" :disabled="!selectedOrderId" @click="startDraftOrder">Edit Draft</button>
            <button class="primary" :disabled="savingOrder || !isAuthenticated" @click="createOrder">
            {{ savingOrder ? "创建中..." : "立即叫车" }}
          </button>
        </div>
        </div>

        <div class="coordinate-grid" @focusin="startDraftOrder">
          <label>
            检索纬度
            <input v-model.number="nearbyForm.lat" type="number" step="0.0001" />
          </label>
          <label>
            检索经度
            <input v-model.number="nearbyForm.lng" type="number" step="0.0001" />
          </label>
          <label>
            半径(米)
            <input v-model.number="nearbyForm.radius_m" type="number" step="100" />
          </label>
          <label>
            数量
            <input v-model.number="nearbyForm.limit" type="number" min="1" max="20" />
          </label>
        </div>

        <div class="order-form" @focusin="startDraftOrder">
          <label>
            上车点地址
            <input v-model="orderForm.pickup_address" placeholder="人民广场" />
          </label>
          <div class="pair">
            <input v-model.number="orderForm.pickup_lat" type="number" step="0.0001" />
            <input v-model.number="orderForm.pickup_lng" type="number" step="0.0001" />
          </div>

          <label>
            目的地地址
            <input v-model="orderForm.destination_address" placeholder="静安寺" />
          </label>
          <div class="pair">
            <input v-model.number="orderForm.destination_lat" type="number" step="0.0001" />
            <input v-model.number="orderForm.destination_lng" type="number" step="0.0001" />
          </div>

          <label>
            预估价格
            <input v-model.number="orderForm.estimated_price" type="number" step="1" />
          </label>
        </div>
      </section>

      <section class="panel orders-panel">
        <div class="panel-head">
          <h2>我的订单</h2>
          <button class="ghost" :disabled="loadingOrders || !isAuthenticated" @click="loadOrders">
            {{ loadingOrders ? "刷新中..." : "刷新" }}
          </button>
        </div>

        <div class="order-list">
          <button
            v-for="order in orders"
            :key="order.id"
            class="order-row"
            :class="{ active: selectedOrderId === order.id }"
            @click="selectOrder(order.id)"
          >
            <strong>{{ order.id }}</strong>
            <span>{{ order.status }}</span>
            <em>{{ formatMoney(order.final_price || order.estimated_price) }}</em>
          </button>
          <p v-if="orders.length === 0" class="empty-hint">还没有订单，先创建一单试试。</p>
        </div>
      </section>

      <section class="panel detail-panel">
        <div class="panel-head">
          <h2>订单详情</h2>
          <div class="actions">
            <button v-if="selectedOrderCanCancel" class="ghost" @click="updateSelectedOrder('cancelled')">取消订单</button>
            <button v-if="selectedOrderCanPay" class="primary" @click="updateSelectedOrder('paid')">模拟支付</button>
          </div>
        </div>

        <div v-if="selectedOrder" class="detail-stack">
          <div class="detail-summary">
            <span class="badge">{{ selectedOrder.status }}</span>
            <strong>{{ selectedOrder.id }}</strong>
            <p>{{ selectedOrder.pickup_address || "未填写地址" }} -> {{ selectedOrder.destination_address || "未填写地址" }}</p>
          </div>

          <dl class="detail-grid">
            <div>
              <dt>司机</dt>
              <dd>{{ selectedOrder.driver_id || "等待派单" }}</dd>
            </div>
            <div>
              <dt>预估价</dt>
              <dd>{{ formatMoney(selectedOrder.estimated_price) }}</dd>
            </div>
            <div>
              <dt>最终价</dt>
              <dd>{{ formatMoney(selectedOrder.final_price) }}</dd>
            </div>
            <div>
              <dt>更新时间</dt>
              <dd>{{ selectedOrder.updated_at }}</dd>
            </div>
          </dl>

          <div class="trip-card">
            <div class="panel-head mini">
              <h3>行程</h3>
              <button class="ghost" :disabled="loadingTrip" @click="loadTrip(selectedOrder.id)">
                {{ loadingTrip ? "加载中..." : "刷新行程" }}
              </button>
            </div>
            <template v-if="selectedTrip">
              <p>状态：{{ selectedTrip.status }}</p>
              <p>里程：{{ selectedTrip.actual_distance_m }} m</p>
              <p>时长：{{ selectedTrip.actual_duration_s }} s</p>
              <p>等待：{{ selectedTrip.waiting_duration_s }} s</p>
              <p>费用：{{ formatMoney(selectedTrip.final_price || selectedTrip.estimated_price) }}</p>
            </template>
            <p v-else class="empty-hint">这单还没有行程数据，通常在司机接单后逐步生成。</p>
          </div>
        </div>
        <p v-else class="empty-hint">左侧选中一条订单，这里会显示更完整的状态和费用信息。</p>
      </section>

      <section class="panel feed-panel">
        <div class="panel-head">
          <h2>实时事件流</h2>
          <span class="feed-count">{{ socketEvents.length }} 条</span>
        </div>
        <div class="feed-list">
          <article v-for="event in socketEvents" :key="event.id" class="feed-item">{{ event.text }}</article>
          <p v-if="socketEvents.length === 0" class="empty-hint">连接 WebSocket 后，这里会持续滚动位置和派单消息。</p>
        </div>
      </section>
    </main>
  </div>
</template>

<style scoped>
.shell {
  max-width: 1480px;
  margin: 0 auto;
  padding: 32px 20px 56px;
}

.hero {
  display: flex;
  justify-content: space-between;
  gap: 24px;
  padding: 24px 28px;
  border: 1px solid var(--line);
  border-radius: 32px;
  background: linear-gradient(135deg, rgba(255, 249, 241, 0.92), rgba(248, 241, 232, 0.86));
  box-shadow: var(--shadow);
}

.eyebrow {
  margin: 0 0 8px;
  color: var(--accent-deep);
  letter-spacing: 0.12em;
  text-transform: uppercase;
  font-size: 12px;
  font-weight: 700;
}

h1 {
  margin: 0;
  font-size: clamp(34px, 4vw, 64px);
  line-height: 0.95;
}

.hero-copy {
  max-width: 760px;
  color: var(--muted);
  line-height: 1.6;
}

.status-cluster {
  display: grid;
  gap: 12px;
  min-width: 220px;
}

.status-pill,
.account-pill {
  display: flex;
  align-items: center;
  gap: 10px;
  justify-content: space-between;
  padding: 14px 16px;
  border-radius: 18px;
  background: rgba(255, 255, 255, 0.72);
  border: 1px solid var(--line);
}

.status-pill .dot {
  width: 10px;
  height: 10px;
  border-radius: 999px;
  background: var(--warn);
  box-shadow: 0 0 0 6px rgba(201, 138, 22, 0.16);
}

.status-pill[data-state="open"] .dot {
  background: var(--ok);
  box-shadow: 0 0 0 6px rgba(31, 143, 99, 0.14);
}

.grid {
  display: grid;
  grid-template-columns: 1.05fr 1.3fr 1.1fr;
  gap: 18px;
  margin-top: 20px;
}

.panel {
  padding: 22px;
  border-radius: 28px;
  border: 1px solid var(--line);
  background: var(--panel);
  box-shadow: var(--shadow);
  backdrop-filter: blur(18px);
}

.map-panel,
.detail-panel {
  grid-column: span 2;
}

.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 18px;
}

.panel-head.mini {
  margin-bottom: 10px;
}

.panel-head h2,
.panel-head h3 {
  margin: 0;
}

.toggle {
  display: inline-flex;
  padding: 4px;
  border-radius: 999px;
  background: rgba(31, 41, 51, 0.06);
}

.toggle button,
.ghost,
.secondary,
.primary,
.order-row {
  transition: transform 180ms ease, background-color 180ms ease, color 180ms ease, border-color 180ms ease;
}

.toggle button {
  border-radius: 999px;
  padding: 8px 14px;
  background: transparent;
  color: var(--muted);
}

.toggle button.active,
.ghost.active {
  background: white;
  color: var(--ink);
}

label {
  display: grid;
  gap: 8px;
  margin-bottom: 14px;
  font-size: 14px;
  color: var(--muted);
}

input {
  width: 100%;
  padding: 12px 14px;
  border-radius: 16px;
  border: 1px solid rgba(31, 41, 51, 0.14);
  background: rgba(255, 255, 255, 0.8);
  color: var(--ink);
}

input:focus {
  outline: 2px solid rgba(242, 107, 58, 0.22);
  border-color: rgba(242, 107, 58, 0.5);
}

.primary,
.secondary,
.ghost {
  padding: 12px 16px;
  border-radius: 16px;
}

.primary {
  background: linear-gradient(135deg, var(--accent), #ff9b56);
  color: white;
}

.secondary {
  background: rgba(31, 41, 51, 0.06);
  color: var(--ink);
}

.ghost {
  background: transparent;
  color: var(--accent-deep);
  border: 1px solid rgba(242, 107, 58, 0.24);
}

.primary:hover,
.secondary:hover,
.ghost:hover,
.order-row:hover,
.toggle button:hover {
  transform: translateY(-1px);
}

.message-box,
.session-card,
.trip-card,
.detail-summary {
  padding: 16px;
  border-radius: 20px;
  border: 1px solid var(--line);
  background: var(--panel-strong);
}

.message-box {
  margin-top: 16px;
  line-height: 1.6;
  color: var(--muted);
}

.session-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}

.session-card p {
  margin: 6px 0 0;
  color: var(--muted);
}

.stack-buttons {
  display: grid;
  gap: 10px;
}

.coordinate-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

.driver-strip {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 18px;
}

.driver-chip {
  display: grid;
  gap: 4px;
  min-width: 150px;
  padding: 12px 14px;
  border-radius: 18px;
  background: rgba(255, 255, 255, 0.75);
  border: 1px solid var(--line);
}

.driver-chip span,
.driver-chip em {
  color: var(--muted);
  font-style: normal;
}

.order-form .pair {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
  margin-bottom: 14px;
}

.order-list {
  display: grid;
  gap: 10px;
}

.order-row {
  display: grid;
  grid-template-columns: 1.4fr auto auto;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 14px 16px;
  border-radius: 18px;
  border: 1px solid var(--line);
  background: rgba(255, 255, 255, 0.68);
  text-align: left;
}

.order-row.active {
  border-color: rgba(242, 107, 58, 0.42);
  background: rgba(242, 107, 58, 0.1);
}

.order-row span,
.order-row em,
.feed-count,
.empty-hint,
.detail-grid dt,
.detail-summary p {
  color: var(--muted);
}

.detail-stack {
  display: grid;
  gap: 16px;
}

.badge {
  display: inline-flex;
  align-items: center;
  width: fit-content;
  padding: 6px 10px;
  margin-bottom: 10px;
  border-radius: 999px;
  background: var(--accent-soft);
  color: var(--accent-deep);
  font-size: 12px;
  font-weight: 700;
  text-transform: uppercase;
}

.detail-summary strong {
  display: block;
  font-size: 20px;
}

.detail-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
  margin: 0;
}

.detail-grid div {
  padding: 14px 16px;
  border-radius: 18px;
  background: rgba(255, 255, 255, 0.72);
  border: 1px solid var(--line);
}

.detail-grid dt {
  margin-bottom: 6px;
  font-size: 13px;
}

.detail-grid dd {
  margin: 0;
  font-weight: 700;
}

.actions {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}

.feed-list {
  display: grid;
  gap: 10px;
  max-height: 420px;
  overflow: auto;
}

.feed-item {
  padding: 14px 16px;
  border-radius: 18px;
  background: rgba(255, 255, 255, 0.72);
  border: 1px solid var(--line);
  line-height: 1.5;
}

@media (max-width: 1180px) {
  .grid {
    grid-template-columns: 1fr;
  }

  .map-panel,
  .detail-panel {
    grid-column: span 1;
  }

  .coordinate-grid,
  .detail-grid {
    grid-template-columns: 1fr 1fr;
  }
}

@media (max-width: 720px) {
  .shell {
    padding: 18px 14px 36px;
  }

  .hero {
    flex-direction: column;
    border-radius: 24px;
    padding: 20px;
  }

  .coordinate-grid,
  .detail-grid,
  .order-form .pair {
    grid-template-columns: 1fr;
  }

  .panel {
    padding: 18px;
    border-radius: 22px;
  }
}
</style>
