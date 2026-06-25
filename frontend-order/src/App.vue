<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from "vue";

import DriverMap from "./components/DriverMap.vue";
import {
  bearingDegrees,
  clampStepMeters,
  dedupePath,
  distanceMeters,
  followPath,
  metersToLat,
  metersToLng,
  trimPathFromCurrent,
} from "./sim";
import type {
  DispatchAssignment,
  DriverLocation,
  DriverProfile,
  DriverRoute,
  LoginResult,
  Order,
  RoutePoint,
  SocketEvent,
  User,
} from "./types";

const storageKey = "driver-debug-session";
const movementTickMs = 400;
const heartbeatTickMs = 5000;
const syncTickMs = 2000;
const idleSnapThresholdM = 25;
const gpsUploadMinIntervalMs = 1000;

const authMode = ref<"login" | "register">("login");
const savingAuth = ref(false);
const busy = ref(false);
const mapReady = ref(false);
const locationSource = ref<"simulation" | "gps">("simulation");
const gpsState = ref<"idle" | "requesting" | "active" | "error" | "unsupported">("idle");
const gpsLastFixAt = ref("");
const liveFareByOrder = ref<Record<string, number>>({});
const ws = ref<WebSocket | null>(null);
const message = ref("司机调试台已就绪。车辆会自动沿路线移动，但接单、到达上车点、乘客上车和送达需要你手动确认。");

const session = reactive<{
  token: string;
  user: User | null;
}>({
  token: "",
  user: null,
});

const authForm = reactive({
  phone: "13900000001",
  password: "pass123456",
  display_name: "Driver Debug",
  plate_no: "",
});

const driverConfig = reactive({
  plate_no: "",
  base_lat: 31.2304,
  base_lng: 121.4737,
  idle_radius_m: 600,
  idle_speed_kph: 28,
  active_speed_kph: 52,
});

const runtime = reactive({
  driver_status: "offline",
  movement_mode: "paused",
  route_mode: "",
  route_order_id: "",
  idle_phase: 0,
  in_trip_started_at: 0,
  arrived_at: 0,
  trip_distance_m: 0,
});

const driverLocation = ref<DriverLocation | null>(null);
const currentOrder = ref<Order | null>(null);
const dispatches = ref<DispatchAssignment[]>([]);
const activeRoute = ref<DriverRoute | null>(null);
const idleRoute = ref<DriverRoute | null>(null);
const movementPath = ref<RoutePoint[]>([]);
const routeTarget = ref<RoutePoint | null>(null);
const selectedDispatchId = ref("");

let movementTimer: number | null = null;
let heartbeatTimer: number | null = null;
let syncTimer: number | null = null;
let gpsWatchID: number | null = null;
let gpsUploadInFlight = false;
let gpsUploadPending = false;
let gpsLastUploadAt = 0;
let gpsUploadTimer: number | null = null;

const isAuthenticated = computed(() => Boolean(session.token && session.user?.driver_id));
const driverID = computed(() => session.user?.driver_id ?? "");
const selectedDispatch = computed(
  () => dispatches.value.find((item) => item.dispatch.id === selectedDispatchId.value) ?? dispatches.value[0] ?? null,
);
const focusOrder = computed(() => currentOrder.value ?? selectedDispatch.value?.order ?? null);
const displayRoute = computed(() => (currentOrder.value ? activeRoute.value : idleRoute.value));
const currentOrderPrice = computed(() => {
  const order = currentOrder.value;
  if (!order) {
    return undefined;
  }
  return liveFareByOrder.value[order.id] ?? order.final_price ?? 0;
});
const currentOrderPickupPoint = computed(() => {
  const order = currentOrder.value;
  if (!order) {
    return null;
  }

  if (activeRoute.value?.mode === "pickup" && activeRoute.value.points.length > 0) {
    return activeRoute.value.points[activeRoute.value.points.length - 1];
  }

  return {
    lat: order.pickup_lat,
    lng: order.pickup_lng,
  };
});
const currentOrderDestinationPoint = computed(() => {
  const order = currentOrder.value;
  if (!order) {
    return null;
  }

  if (activeRoute.value?.mode === "trip" && activeRoute.value.points.length > 0) {
    return activeRoute.value.points[activeRoute.value.points.length - 1];
  }

  return {
    lat: order.destination_lat,
    lng: order.destination_lng,
  };
});
const currentOrderPickupSummary = computed(() => currentOrder.value?.pickup_address || "未填写上车点地址");
const currentOrderDestinationSummary = computed(() => currentOrder.value?.destination_address || "未填写目的地地址");
const pickupLat = computed(() => focusOrder.value?.pickup_lat ?? null);
const pickupLng = computed(() => focusOrder.value?.pickup_lng ?? null);
const destinationLat = computed(() => focusOrder.value?.destination_lat ?? null);
const destinationLng = computed(() => focusOrder.value?.destination_lng ?? null);
const distanceToPickup = computed(() => {
  if (!driverLocation.value || !focusOrder.value) {
    return "--";
  }

  return formatDistance(
    distanceMeters(
      driverLocation.value.lat,
      driverLocation.value.lng,
      focusOrder.value.pickup_lat,
      focusOrder.value.pickup_lng,
    ),
  );
});
const distanceToDestination = computed(() => {
  if (!driverLocation.value || !focusOrder.value) {
    return "--";
  }

  return formatDistance(
    distanceMeters(
      driverLocation.value.lat,
      driverLocation.value.lng,
      focusOrder.value.destination_lat,
      focusOrder.value.destination_lng,
    ),
  );
});

onMounted(() => {
  restoreSession();
  if (isAuthenticated.value) {
    void bootstrapDriver();
  }
});

onBeforeUnmount(() => {
  disconnectSocket();
  stopGPSWatch();
  stopLoops();
});

async function bootstrapDriver() {
  if (!(await ensureSessionValid())) {
    return;
  }

  await hydrateDriverProfile();
  await setDriverStatus("online");
  await hydrateDriverLocation();
  await syncDriverState();
  connectSocket();
  startLoops();
}

function connectSocket() {
  if (!session.token || ws.value?.readyState === WebSocket.OPEN) {
    return;
  }

  disconnectSocket();
  const protocol = window.location.protocol === "https:" ? "wss" : "ws";
  const socket = new WebSocket(
    `${protocol}://${window.location.host}/ws/location?token=${encodeURIComponent(session.token)}`,
  );
  ws.value = socket;
  socket.onmessage = (event) => {
    try {
      handleSocketEvent(JSON.parse(event.data) as SocketEvent);
    } catch {
      // Ignore non-JSON debug messages.
    }
  };
  socket.onclose = () => {
    if (ws.value === socket) {
      ws.value = null;
    }
  };
}

function disconnectSocket() {
  ws.value?.close();
  ws.value = null;
}

function handleSocketEvent(event: SocketEvent) {
  if (event.type !== "trip.fare.updated" || !isRecord(event.data)) {
    return;
  }

  const orderID = String(event.data.order_id ?? "");
  const currentPrice = Number(event.data.current_price ?? 0);
  if (!orderID || !Number.isFinite(currentPrice) || currentPrice < 0) {
    return;
  }
  liveFareByOrder.value = {
    ...liveFareByOrder.value,
    [orderID]: currentPrice,
  };
}

async function hydrateDriverProfile() {
  if (!driverID.value) {
    return;
  }

  const result = await api<{ item: DriverProfile }>(`/api/v1/drivers/${driverID.value}/vehicle`);
  driverConfig.plate_no = result.item.plate_no ?? "";
}

async function ensureSessionValid() {
  if (!session.token) {
    return false;
  }

  try {
    const result = await api<{ item: User }>("/api/v1/auth/me");
    session.user = result.item;
    persistSession();
    return true;
  } catch {
    handleUnauthorized();
    return false;
  }
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
          role: "driver",
          display_name: authForm.display_name,
          plate_no: authForm.plate_no,
          device_type: "driver-web",
        },
      });
      message.value = "司机账号注册成功，正在登录。";
    }

    const result = await api<{ item: LoginResult }>("/api/v1/auth/login", {
      method: "POST",
      body: {
        phone: authForm.phone,
        password: authForm.password,
        device_type: "driver-web",
      },
    });

    session.token = result.item.token;
    session.user = result.item.user;
    persistSession();
    message.value = `司机 ${result.item.user.phone} 已登录，正在进入手动调试模式。`;
    await bootstrapDriver();
  } catch (error) {
    message.value = asErrorMessage(error);
  } finally {
    savingAuth.value = false;
  }
}

async function hydrateDriverLocation() {
  if (!driverID.value) {
    return;
  }

  try {
    const result = await api<{ item: DriverLocation }>(`/api/v1/drivers/${driverID.value}/location`);
    driverLocation.value = result.item;
  } catch {
    driverLocation.value = {
      driver_id: driverID.value,
      lat: driverConfig.base_lat,
      lng: driverConfig.base_lng,
      heading: 0,
      speed_kph: 0,
    };
    await pushLocationUpdate();
  }
}

function startLoops() {
  stopLoops();
  movementTimer = window.setInterval(() => {
    void movementTick();
  }, movementTickMs);
  heartbeatTimer = window.setInterval(() => {
    void sendHeartbeat();
  }, heartbeatTickMs);
  syncTimer = window.setInterval(() => {
    void syncDriverState();
  }, syncTickMs);
}

function stopLoops() {
  if (movementTimer !== null) {
    window.clearInterval(movementTimer);
    movementTimer = null;
  }
  if (heartbeatTimer !== null) {
    window.clearInterval(heartbeatTimer);
    heartbeatTimer = null;
  }
  if (syncTimer !== null) {
    window.clearInterval(syncTimer);
    syncTimer = null;
  }
}

async function syncDriverState() {
  if (!isAuthenticated.value) {
    return;
  }

  currentOrder.value = await fetchCurrentOrder();
  dispatches.value = await fetchDispatches();

  if (!selectedDispatchId.value && dispatches.value.length > 0) {
    selectedDispatchId.value = dispatches.value[0].dispatch.id;
  }
  if (selectedDispatchId.value && !dispatches.value.some((item) => item.dispatch.id === selectedDispatchId.value)) {
    selectedDispatchId.value = dispatches.value[0]?.dispatch.id ?? "";
  }

  if (currentOrder.value) {
    await syncActiveRoute(currentOrder.value);
  } else {
    activeRoute.value = null;
    runtime.route_mode = "";
    runtime.route_order_id = "";
    await ensureIdleRoute();
  }
}

async function fetchCurrentOrder() {
  if (!driverID.value) {
    return null;
  }

  try {
    const result = await api<{ item: Order }>(`/api/v1/drivers/${driverID.value}/current-order`);
    return result.item;
  } catch (error) {
    const text = asErrorMessage(error);
    if (text.includes("not found")) {
      return null;
    }
    throw error;
  }
}

async function fetchDispatches(): Promise<DispatchAssignment[]> {
  if (!driverID.value) {
    return [];
  }

  try {
    const result = await api<{ items: DispatchAssignment[] }>(`/api/v1/drivers/${driverID.value}/dispatches`);
    return result.items;
  } catch (error) {
    message.value = `拉取派单失败：${asErrorMessage(error)}`;
    return [];
  }
}

async function syncActiveRoute(order: Order) {
  try {
    const result = await api<{ item: DriverRoute }>(`/api/v1/orders/${order.id}/route`);
    activeRoute.value = result.item;
    runtime.route_mode = result.item.mode;
    runtime.route_order_id = order.id;
    prepareMovementPath(result.item, order.id);
  } catch (error) {
    message.value = `拉取订单路线失败：${asErrorMessage(error)}`;
  }
}

function prepareMovementPath(route: DriverRoute, orderID: string) {
  const current = driverLocation.value;
  const points = dedupePath(route.points ?? []);
  if (points.length === 0 || !current) {
    movementPath.value = [];
    routeTarget.value = null;
    return;
  }

  routeTarget.value = points[points.length - 1];
  movementPath.value = trimPathFromCurrent(points, { lat: current.lat, lng: current.lng });
  runtime.route_mode = route.mode;
  runtime.route_order_id = orderID;
}

async function ensureIdleRoute() {
  if (!driverLocation.value || currentOrder.value) {
    return;
  }
  if (runtime.movement_mode === "idle" && movementPath.value.length > 1) {
    return;
  }

  runtime.idle_phase += Math.PI / 3;
  const destinationLat = driverConfig.base_lat + metersToLat(driverConfig.idle_radius_m * Math.cos(runtime.idle_phase));
  const destinationLng = driverConfig.base_lng + metersToLng(driverConfig.idle_radius_m * Math.sin(runtime.idle_phase), driverConfig.base_lat);

  try {
    const query = new URLSearchParams({
      origin_lat: String(driverLocation.value.lat),
      origin_lng: String(driverLocation.value.lng),
      destination_lat: String(destinationLat),
      destination_lng: String(destinationLng),
    });
    const result = await api<{ item: DriverRoute }>(`/api/v1/routes/preview?${query.toString()}`);
    idleRoute.value = {
      ...result.item,
      mode: "idle",
    };
    const points = dedupePath(result.item.points ?? []);
    if (points.length === 0) {
      return;
    }
    if (distanceMeters(driverLocation.value.lat, driverLocation.value.lng, points[0].lat, points[0].lng) > idleSnapThresholdM) {
      driverLocation.value = {
        ...driverLocation.value,
        lat: points[0].lat,
        lng: points[0].lng,
      };
      await pushLocationUpdate();
    }
    movementPath.value = trimPathFromCurrent(points, { lat: driverLocation.value.lat, lng: driverLocation.value.lng });
    routeTarget.value = points[points.length - 1];
    runtime.movement_mode = "idle";
    runtime.route_mode = "idle";
    runtime.route_order_id = "";
  } catch (error) {
    message.value = `生成空闲巡航路线失败：${asErrorMessage(error)}`;
  }
}

async function movementTick() {
  if (
    locationSource.value !== "simulation"
    || !isAuthenticated.value
    || !driverLocation.value
    || runtime.driver_status === "offline"
  ) {
    return;
  }

  if (currentOrder.value && (!activeRoute.value || runtime.route_order_id !== currentOrder.value.id)) {
    await syncActiveRoute(currentOrder.value);
  }
  if (!currentOrder.value && movementPath.value.length === 0) {
    await ensureIdleRoute();
  }

  if (movementPath.value.length === 0) {
    return;
  }

  const speedKph = currentOrder.value ? driverConfig.active_speed_kph : driverConfig.idle_speed_kph;
  const stepM = clampStepMeters(speedKph, movementTickMs);
  const current = driverLocation.value;
  const moved = followPath(current.lat, current.lng, movementPath.value, stepM);
  const heading = bearingDegrees(current.lat, current.lng, moved.lat, moved.lng);

  driverLocation.value = {
    driver_id: driverID.value,
    order_id: currentOrder.value?.id,
    lat: moved.lat,
    lng: moved.lng,
    heading,
    speed_kph: speedKph,
    accuracy_m: 5,
  };
  movementPath.value = moved.path;
  runtime.movement_mode = currentOrder.value ? currentOrder.value.status : "idle";

  if (currentOrder.value?.status === "in_trip") {
    runtime.trip_distance_m += moved.traveledM;
  }

  await pushLocationUpdate();

  if (!currentOrder.value && movementPath.value.length === 0) {
    await ensureIdleRoute();
  }
}

function useSimulationLocation() {
  stopGPSWatch();
  locationSource.value = "simulation";
  runtime.movement_mode = currentOrder.value?.status ?? "idle";
  message.value = "已切换为模拟行驶，车辆将继续沿规划路线自动移动。";
}

function useGPSLocation() {
  if (!("geolocation" in navigator)) {
    gpsState.value = "unsupported";
    message.value = "当前浏览器不支持 GPS 定位。";
    return;
  }
  if (!isAuthenticated.value) {
    message.value = "请先登录司机账号，再启用 GPS 定位。";
    return;
  }

  stopGPSWatch();
  locationSource.value = "gps";
  gpsState.value = "requesting";
  runtime.movement_mode = "gps_waiting";
  message.value = "正在申请 GPS 定位权限，请在浏览器中允许访问位置。";

  gpsWatchID = navigator.geolocation.watchPosition(
    handleGPSPosition,
    handleGPSError,
    {
      enableHighAccuracy: true,
      maximumAge: 1000,
      timeout: 15000,
    },
  );
}

function stopGPSWatch() {
  if (gpsWatchID !== null && "geolocation" in navigator) {
    navigator.geolocation.clearWatch(gpsWatchID);
  }
  gpsWatchID = null;
  if (gpsUploadTimer !== null) {
    window.clearTimeout(gpsUploadTimer);
    gpsUploadTimer = null;
  }
  gpsUploadPending = false;
  gpsUploadInFlight = false;
  if (gpsState.value !== "unsupported") {
    gpsState.value = "idle";
  }
}

function handleGPSPosition(position: GeolocationPosition) {
  if (locationSource.value !== "gps" || !driverID.value) {
    return;
  }

  const lat = position.coords.latitude;
  const lng = position.coords.longitude;
  if (!Number.isFinite(lat) || !Number.isFinite(lng)) {
    return;
  }

  const previous = driverLocation.value;
  const traveledM = previous ? distanceMeters(previous.lat, previous.lng, lat, lng) : 0;
  const timestamp = new Date(position.timestamp).toISOString();
  const heading = position.coords.heading ?? (
    previous && traveledM > 0.5
      ? bearingDegrees(previous.lat, previous.lng, lat, lng)
      : previous?.heading ?? 0
  );

  driverLocation.value = {
    driver_id: driverID.value,
    order_id: currentOrder.value?.id,
    lat,
    lng,
    heading,
    speed_kph: Math.max(0, (position.coords.speed ?? 0) * 3.6),
    accuracy_m: position.coords.accuracy,
    timestamp,
  };
  gpsState.value = "active";
  gpsLastFixAt.value = timestamp;
  runtime.movement_mode = currentOrder.value?.status ?? "gps";

  if (currentOrder.value?.status === "in_trip" && traveledM < 500) {
    runtime.trip_distance_m += traveledM;
  }

  scheduleGPSUpload();
}

function handleGPSError(error: GeolocationPositionError) {
  gpsState.value = "error";
  const reason = error.code === error.PERMISSION_DENIED
    ? "定位权限被拒绝"
    : error.code === error.POSITION_UNAVAILABLE
      ? "暂时无法获取位置"
      : "获取位置超时";
  message.value = `GPS 定位失败：${reason}。`;
}

function scheduleGPSUpload() {
  if (gpsUploadInFlight) {
    gpsUploadPending = true;
    return;
  }

  const waitMs = Math.max(0, gpsUploadMinIntervalMs - (Date.now() - gpsLastUploadAt));
  if (waitMs > 0) {
    if (gpsUploadTimer === null) {
      gpsUploadTimer = window.setTimeout(() => {
        gpsUploadTimer = null;
        void uploadGPSLocation();
      }, waitMs);
    }
    return;
  }
  void uploadGPSLocation();
}

async function uploadGPSLocation() {
  if (locationSource.value !== "gps" || !driverLocation.value || runtime.driver_status === "offline") {
    return;
  }

  gpsUploadInFlight = true;
  gpsUploadPending = false;
  try {
    await pushLocationUpdate();
    gpsLastUploadAt = Date.now();
  } catch (error) {
    message.value = `GPS 位置上报失败：${asErrorMessage(error)}`;
  } finally {
    gpsUploadInFlight = false;
    if (gpsUploadPending) {
      scheduleGPSUpload();
    }
  }
}

async function pushLocationUpdate() {
  if (!driverID.value || !driverLocation.value) {
    return;
  }

  await api(`/api/v1/drivers/${driverID.value}/location`, {
    method: "POST",
    body: {
      order_id: driverLocation.value.order_id ?? "",
      lat: driverLocation.value.lat,
      lng: driverLocation.value.lng,
      speed_kph: driverLocation.value.speed_kph ?? 0,
      heading: driverLocation.value.heading ?? 0,
      accuracy_m: driverLocation.value.accuracy_m ?? 5,
    },
  });
}

async function sendHeartbeat() {
  if (!driverID.value) {
    return;
  }

  try {
    await api(`/api/v1/drivers/${driverID.value}/heartbeat`, {
      method: "POST",
      body: {
        order_id: currentOrder.value?.id ?? "",
      },
    });
  } catch (error) {
    message.value = `心跳发送失败：${asErrorMessage(error)}`;
  }
}

async function setDriverStatus(status: string) {
  if (!driverID.value) {
    return;
  }

  busy.value = true;
  try {
    await api(`/api/v1/drivers/${driverID.value}/status`, {
      method: "POST",
      body: { status },
    });
    runtime.driver_status = status;
    message.value = `司机状态已切换为 ${status}。`;
  } catch (error) {
    message.value = asErrorMessage(error);
  } finally {
    busy.value = false;
  }
}

async function upsertVehicle() {
  if (!driverID.value) {
    return;
  }

  await api(`/api/v1/drivers/${driverID.value}/vehicle`, {
    method: "POST",
    body: { plate_no: driverConfig.plate_no },
  });
  message.value = `车牌已更新为 ${driverConfig.plate_no}。`;
}

async function acceptDispatch(assignment: DispatchAssignment) {
  if (!assignment.order?.id) {
    return;
  }

  busy.value = true;
  try {
    const result = await api<{ item: Order }>(`/api/v1/orders/${assignment.order.id}/status`, {
      method: "POST",
      body: {
        status: "accepted",
        driver_id: driverID.value,
      },
    });
    currentOrder.value = result.item;
    runtime.arrived_at = 0;
    runtime.in_trip_started_at = 0;
    runtime.trip_distance_m = 0;
    await syncActiveRoute(result.item);
    message.value = `已手动接单 ${result.item.id}，车辆将自动沿后端路线前往上车点。`;
    await syncDriverState();
  } catch (error) {
    message.value = `接单失败：${asErrorMessage(error)}`;
  } finally {
    busy.value = false;
  }
}

async function markArrived() {
  if (!currentOrder.value) {
    return;
  }

  await updateCurrentOrderStatus(
    "driver_arrived",
    () => {
      runtime.arrived_at = Date.now();
    },
    "已确认到达上车点，等待乘客上车。",
  );
}

async function markPassengerPickedUp() {
  if (!currentOrder.value) {
    return;
  }

  await updateCurrentOrderStatus(
    "in_trip",
    () => {
      runtime.in_trip_started_at = Date.now();
      runtime.trip_distance_m = 0;
    },
    "已确认乘客上车，车辆将自动沿后端路线前往目的地。",
  );
}

async function markTripCompleted() {
  if (!currentOrder.value) {
    return;
  }

  const actualDurationS =
    runtime.in_trip_started_at > 0 ? Math.max(1, Math.round((Date.now() - runtime.in_trip_started_at) / 1000)) : 0;
  const waitingDurationS =
    runtime.arrived_at > 0 && runtime.in_trip_started_at > runtime.arrived_at
      ? Math.max(0, Math.round((runtime.in_trip_started_at - runtime.arrived_at) / 1000))
      : 0;

  busy.value = true;
  try {
    const result = await api<{ item: Order }>(`/api/v1/orders/${currentOrder.value.id}/status`, {
      method: "POST",
      body: {
        status: "completed",
        actual_distance_m: Math.round(runtime.trip_distance_m),
        actual_duration_s: actualDurationS,
        waiting_duration_s: waitingDurationS,
      },
    });
    message.value = `已确认送达，订单 ${result.item.id} 已完成。`;
    currentOrder.value = null;
    activeRoute.value = null;
    movementPath.value = [];
    routeTarget.value = null;
    runtime.trip_distance_m = 0;
    runtime.arrived_at = 0;
    runtime.in_trip_started_at = 0;
    await syncDriverState();
  } catch (error) {
    message.value = `完成订单失败：${asErrorMessage(error)}`;
  } finally {
    busy.value = false;
  }
}

async function updateCurrentOrderStatus(status: string, onSuccess: () => void, successMessage: string) {
  if (!currentOrder.value) {
    return;
  }

  busy.value = true;
  try {
    const result = await api<{ item: Order }>(`/api/v1/orders/${currentOrder.value.id}/status`, {
      method: "POST",
      body: { status },
    });
    currentOrder.value = result.item;
    onSuccess();
    await syncActiveRoute(result.item);
    message.value = successMessage;
  } catch (error) {
    message.value = asErrorMessage(error);
  } finally {
    busy.value = false;
  }
}

function persistSession() {
  localStorage.setItem(
    storageKey,
    JSON.stringify({
      token: session.token,
      user: session.user,
    }),
  );
}

function restoreSession() {
  try {
    const raw = localStorage.getItem(storageKey);
    if (!raw) {
      return;
    }
    const parsed = JSON.parse(raw) as { token?: string; user?: User };
    session.token = parsed.token ?? "";
    session.user = parsed.user ?? null;
  } catch {
    localStorage.removeItem(storageKey);
  }
}

function logout() {
  disconnectSocket();
  stopGPSWatch();
  stopLoops();
  session.token = "";
  session.user = null;
  driverLocation.value = null;
  currentOrder.value = null;
  dispatches.value = [];
  activeRoute.value = null;
  idleRoute.value = null;
  movementPath.value = [];
  routeTarget.value = null;
  locationSource.value = "simulation";
  gpsLastFixAt.value = "";
  liveFareByOrder.value = {};
  runtime.driver_status = "offline";
  localStorage.removeItem(storageKey);
  message.value = "已退出司机调试台。";
}

function handleUnauthorized() {
  logout();
  message.value = "登录状态已失效，请重新登录。";
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
  if (response.status === 401) {
    handleUnauthorized();
    throw new Error(data.error ?? "登录状态已失效，请重新登录。");
  }
  if (!response.ok) {
    throw new Error(data.error ?? `请求失败: ${response.status}`);
  }
  return data as T;
}

function asErrorMessage(error: unknown) {
  return error instanceof Error ? error.message : "发生未知错误";
}

function formatDistance(distanceM: number) {
  if (!Number.isFinite(distanceM) || distanceM < 0) {
    return "--";
  }
  if (distanceM >= 1000) {
    return `${(distanceM / 1000).toFixed(2)}km`;
  }
  return `${Math.round(distanceM)}m`;
}

function formatMoney(value?: number) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return "-";
  }
  return `¥${value.toFixed(2)}`;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}
</script>

<template>
  <div class="shell">
    <header class="hero">
      <div class="hero-brand">
        <h1>Driver Debug 调试台</h1>
      </div>

      <div class="hero-nav">
        <div class="toggle nav-toggle">
          <button :class="{ active: authMode === 'login' }" @click="authMode = 'login'">登录</button>
          <button :class="{ active: authMode === 'register' }" @click="authMode = 'register'">注册</button>
        </div>

        <template v-if="!isAuthenticated">
          <div class="inline-auth-form">
            <input v-model="authForm.phone" placeholder="手机号" />
            <input v-if="authMode === 'register'" v-model="authForm.display_name" placeholder="司机昵称" />
            <input v-model="authForm.password" type="password" placeholder="密码" />
            <input v-if="authMode === 'register'" v-model="authForm.plate_no" placeholder="车牌号" />
            <button class="primary inline-auth-submit" :disabled="savingAuth" @click="submitAuth">
              {{ savingAuth ? "提交中..." : authMode === "login" ? "登录司机端" : "注册并登录" }}
            </button>
          </div>
        </template>

        <template v-else>
          <div class="nav-account">
            <div class="nav-account-copy">
              <strong>{{ driverID }}</strong>
              <span>{{ session.user?.phone }}</span>
            </div>
            <div class="nav-meta">
              <span>{{ driverConfig.plate_no }}</span>
            </div>
            <button class="ghost nav-logout" @click="logout">退出</button>
          </div>
        </template>
      </div>
    </header>

    <main class="grid">
      <section class="panel map-panel">
        <div class="panel-head">
          <h2>司机地图</h2>
          <div class="panel-tools">
            <div class="status-cluster map-status-cluster">
              <div class="status-pill" :data-state="isAuthenticated ? 'open' : 'closed'">
                <span class="dot"></span>
                Auth: {{ isAuthenticated ? "ready" : "idle" }}
              </div>
              <div class="status-pill" :data-state="mapReady ? 'open' : 'closed'">
                <span class="dot"></span>
                Map: {{ mapReady ? "ready" : "waiting" }}
              </div>
            </div>
            <div class="actions">
              <div class="toggle location-source-toggle" aria-label="定位来源">
                <button
                  :class="{ active: locationSource === 'simulation' }"
                  :disabled="!isAuthenticated"
                  @click="useSimulationLocation"
                >
                  模拟行驶
                </button>
                <button
                  :class="{ active: locationSource === 'gps' }"
                  :disabled="!isAuthenticated"
                  @click="useGPSLocation"
                >
                  GPS 定位
                </button>
              </div>
              <button class="secondary" :disabled="!isAuthenticated || busy" @click="syncDriverState">刷新司机状态</button>
              <button class="secondary" :disabled="!isAuthenticated || busy" @click="setDriverStatus('online')">上线</button>
              <button class="ghost" :disabled="!isAuthenticated || busy" @click="setDriverStatus('offline')">下线</button>
            </div>
          </div>
        </div>

        <div class="message-box">{{ message }}</div>

        <DriverMap
          :driver-location="driverLocation"
          :route-data="displayRoute"
          :pickup-lat="pickupLat"
          :pickup-lng="pickupLng"
          :destination-lat="destinationLat"
          :destination-lng="destinationLng"
          @ready="mapReady = true"
          @error="message = $event"
        />

        <div class="metrics">
          <article class="metric-card">
            <span>司机状态</span>
            <strong>{{ runtime.driver_status }}</strong>
          </article>
          <article class="metric-card">
            <span>当前状态</span>
            <strong>{{ runtime.movement_mode }}</strong>
          </article>
          <article class="metric-card">
            <span>定位来源</span>
            <strong>{{ locationSource === "gps" ? `GPS · ${gpsState}` : "模拟行驶" }}</strong>
            <em v-if="gpsLastFixAt">最近定位 {{ new Date(gpsLastFixAt).toLocaleTimeString() }}</em>
          </article>
          <article class="metric-card">
            <span>距上车点</span>
            <strong>{{ distanceToPickup }}</strong>
          </article>
          <article class="metric-card">
            <span>距目的地</span>
            <strong>{{ distanceToDestination }}</strong>
          </article>
        </div>
      </section>

      <section class="panel config-panel">
        <div class="panel-head">
          <h2>调试参数</h2>
          <button class="ghost" :disabled="!isAuthenticated || busy" @click="upsertVehicle">更新车牌</button>
        </div>

        <div class="scroll-window">
          <div class="form-stack">
            <label>
              车牌号
              <input v-model="driverConfig.plate_no" placeholder="沪A90001" />
            </label>
            <div class="pair">
              <label>
                基准纬度
                <input v-model.number="driverConfig.base_lat" type="number" step="0.0001" />
              </label>
              <label>
                基准经度
                <input v-model.number="driverConfig.base_lng" type="number" step="0.0001" />
              </label>
            </div>
            <div class="pair">
              <label>
                空闲半径(米)
                <input v-model.number="driverConfig.idle_radius_m" type="number" step="50" />
              </label>
              <label>
                空闲速度(km/h)
                <input v-model.number="driverConfig.idle_speed_kph" type="number" step="1" />
              </label>
            </div>
            <label>
              接单速度(km/h)
              <input v-model.number="driverConfig.active_speed_kph" type="number" step="1" />
            </label>
          </div>
        </div>
      </section>

      <section class="panel dispatch-panel">
        <div class="panel-head">
          <h2>待接派单</h2>
          <button class="secondary" :disabled="!isAuthenticated || busy" @click="syncDriverState">刷新派单</button>
        </div>

        <div class="scroll-window">
          <div v-if="dispatches.length > 0" class="list-stack">
            <button
              v-for="assignment in dispatches"
              :key="assignment.dispatch.id"
              class="list-row"
              :class="{ active: selectedDispatchId === assignment.dispatch.id }"
              @click="selectedDispatchId = assignment.dispatch.id"
            >
              <div class="dispatch-card-grid">
                <div class="dispatch-card-row">
                  <span class="dispatch-card-label">上车点</span>
                  <strong>{{ assignment.order.pickup_address || "未填写上车点" }}</strong>
                </div>
                <div class="dispatch-card-row">
                  <span class="dispatch-card-label">目的地</span>
                  <strong>{{ assignment.order.destination_address || "未填写目的地" }}</strong>
                </div>
                <div class="dispatch-card-row">
                  <span class="dispatch-card-label">预估价</span>
                  <strong>{{ formatMoney(assignment.order.estimated_price) }}</strong>
                </div>
                <div class="dispatch-card-row">
                  <span class="dispatch-card-label">总路程</span>
                  <strong>
                    {{
                      formatDistance(
                        distanceMeters(
                          assignment.order.pickup_lat,
                          assignment.order.pickup_lng,
                          assignment.order.destination_lat,
                          assignment.order.destination_lng,
                        ),
                      )
                    }}
                  </strong>
                </div>
              </div>
            </button>
          </div>
          <p v-else class="empty-hint">当前没有待接派单。</p>
        </div>

        <button
          class="primary wide"
          :disabled="!selectedDispatch || !!currentOrder || busy"
          @click="selectedDispatch && acceptDispatch(selectedDispatch)"
        >
          确认接收当前派单
        </button>
      </section>

      <section class="panel order-panel">
        <div class="panel-head">
          <h2>当前订单</h2>
        </div>

        <div class="scroll-window">
          <div v-if="currentOrder" class="detail-stack">
            <div class="detail-summary">
              <span class="badge">{{ currentOrder.status }}</span>
              <strong>{{ currentOrder.id }}</strong>
              <p>{{ currentOrderPickupSummary }} -> {{ currentOrderDestinationSummary }}</p>
            </div>

            <dl class="detail-grid">
              <div>
                <dt>乘客ID</dt>
                <dd>{{ currentOrder.passenger_id }}</dd>
              </div>
              <div>
                <dt>实时价格</dt>
                <dd>{{ formatMoney(currentOrderPrice) }}</dd>
              </div>
              <div>
                <dt>预估价</dt>
                <dd>{{ formatMoney(currentOrder.estimated_price) }}</dd>
              </div>
              <div>
                <dt>累计里程</dt>
                <dd>{{ formatDistance(runtime.trip_distance_m) }}</dd>
              </div>
            </dl>

            <div class="actions">
              <button class="secondary" :disabled="currentOrder.status !== 'accepted' || busy" @click="markArrived">到达上车点</button>
              <button class="secondary" :disabled="currentOrder.status !== 'driver_arrived' || busy" @click="markPassengerPickedUp">乘客已上车</button>
              <button class="primary" :disabled="currentOrder.status !== 'in_trip' || busy" @click="markTripCompleted">顾客已送达</button>
            </div>
          </div>
          <p v-else class="empty-hint">当前没有活跃订单，车辆会保持空闲巡航，直到你手动接收一单。</p>
        </div>
      </section>
    </main>
  </div>
</template>

<style scoped>
.shell {
  width: 100%;
  padding: 28px 20px 56px;
}

.hero {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 24px;
  padding: 24px 28px;
  border: 1px solid var(--line);
  border-radius: 32px;
  background: linear-gradient(135deg, rgba(255, 249, 241, 0.92), rgba(248, 241, 232, 0.86));
  box-shadow: var(--shadow);
}

.hero-brand h1,
.panel-head h2 {
  margin: 0;
}

.hero-brand p {
  margin: 12px 0 0;
  max-width: 760px;
  color: var(--muted);
  line-height: 1.6;
}

.hero-nav {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 16px;
  flex-wrap: wrap;
  flex: 0 0 auto;
}

.nav-toggle {
  flex-shrink: 0;
}

.inline-auth-form {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 10px;
  align-items: center;
  min-width: min(860px, 68vw);
}

.inline-auth-submit {
  min-height: 48px;
}

.nav-account {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border-radius: 22px;
  border: 1px solid var(--line);
  background: rgba(255, 255, 255, 0.76);
  min-width: 0;
}

.nav-account-copy,
.nav-meta {
  display: grid;
  min-width: 0;
}

.nav-account-copy strong,
.nav-account-copy span,
.nav-meta span {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.nav-account-copy span,
.nav-meta span {
  color: var(--muted);
  font-size: 13px;
}

.nav-logout {
  padding: 10px 14px;
  flex-shrink: 0;
}

.status-cluster {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: auto;
}

.status-pill {
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

.panel-tools {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 14px;
  flex-wrap: wrap;
}

.map-status-cluster .status-pill {
  min-width: 180px;
  padding: 12px 14px;
}

.grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 18px;
  max-width: 1480px;
  margin: 20px auto 0;
}

.panel {
  padding: 22px;
  border-radius: 28px;
  border: 1px solid var(--line);
  background: var(--panel);
  box-shadow: var(--shadow);
  backdrop-filter: blur(18px);
}

.map-panel {
  grid-column: 1 / -1;
}

.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 18px;
}

.message-box,
.detail-summary {
  padding: 16px;
  border-radius: 20px;
  border: 1px solid var(--line);
  background: var(--panel-strong);
}

.message-box {
  margin-bottom: 18px;
  color: var(--muted);
  line-height: 1.6;
}

.metrics {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 12px;
  margin-top: 18px;
}

.location-source-toggle {
  flex: 0 0 auto;
}

.metric-card {
  display: grid;
  gap: 4px;
  padding: 14px 16px;
  border-radius: 18px;
  background: rgba(255, 255, 255, 0.76);
  border: 1px solid var(--line);
}

.metric-card span,
.metric-card em,
.empty-hint,
.detail-grid dt,
.detail-summary p {
  color: var(--muted);
}

.metric-card strong {
  font-size: 24px;
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
.list-row {
  transition:
    transform 180ms ease,
    background-color 180ms ease,
    color 180ms ease,
    border-color 180ms ease;
}

.toggle button {
  border-radius: 999px;
  padding: 8px 14px;
  background: transparent;
  color: var(--muted);
}

.toggle button.active {
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

.wide {
  width: 100%;
  margin-top: 14px;
}

.actions {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}

.form-stack,
.detail-stack,
.list-stack {
  display: grid;
  gap: 12px;
}

.scroll-window {
  min-height: 320px;
  max-height: 560px;
  padding: 14px;
  border-radius: 22px;
  border: 1px solid var(--line);
  background: rgba(255, 255, 255, 0.56);
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-gutter: stable;
}

.pair {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

.list-row {
  display: block;
  width: 100%;
  padding: 16px 18px;
  border-radius: 18px;
  border: 1px solid var(--line);
  background: rgba(255, 255, 255, 0.68);
  text-align: left;
}

.list-row.active {
  border-color: rgba(242, 107, 58, 0.42);
  background: rgba(242, 107, 58, 0.1);
}

.dispatch-card-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px 32px;
}

.dispatch-card-row {
  display: flex;
  align-items: baseline;
  gap: 10px;
  min-width: 0;
}

.dispatch-card-label {
  flex: 0 0 auto;
  color: var(--ink);
  font-size: 15px;
  line-height: 1.25;
  font-weight: 700;
}

.dispatch-card-row strong {
  flex: 1 1 auto;
  min-width: 0;
  font-size: 15px;
  line-height: 1.25;
  font-weight: 600;
  color: var(--ink);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
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

.detail-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
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

@media (max-width: 1180px) {
  .grid {
    grid-template-columns: 1fr;
  }

  .metrics,
  .detail-grid,
  .pair,
  .dispatch-card-grid {
    grid-template-columns: 1fr;
  }

  .inline-auth-form {
    grid-template-columns: 1fr 1fr;
    min-width: 0;
    width: 100%;
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
    align-items: stretch;
  }

  .hero-nav,
  .panel-tools,
  .status-cluster {
    width: 100%;
  }

  .status-cluster {
    flex-direction: column;
    align-items: stretch;
  }

  .inline-auth-form,
  .nav-account {
    width: 100%;
  }

  .inline-auth-form,
  .pair {
    grid-template-columns: 1fr;
  }
}
</style>
