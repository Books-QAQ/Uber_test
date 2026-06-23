<script setup lang="ts">
import AMapLoader from "@amap/amap-jsapi-loader";
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

import type { DriverLiveLocation, NearbyDriver, Order } from "../types";

type MapClickPayload = {
  mode: "pickup" | "destination";
  lat: number;
  lng: number;
};

const props = defineProps<{
  pickupLat: number;
  pickupLng: number;
  destinationLat: number;
  destinationLng: number;
  drivers: NearbyDriver[];
  liveDriverLocations: Record<string, DriverLiveLocation>;
  pickMode: "pickup" | "destination";
  currentOrder: Order | null;
}>();

const emit = defineEmits<{
  (event: "pick-location", payload: MapClickPayload): void;
  (event: "ready"): void;
  (event: "error", message: string): void;
}>();

const mapHost = ref<HTMLElement | null>(null);
const loadState = ref<"idle" | "loading" | "ready" | "error">("idle");
const errorMessage = ref("");

const amapKey = (import.meta.env.VITE_AMAP_KEY as string | undefined)?.trim() ?? "";
const amapSecurityJsCode = (import.meta.env.VITE_AMAP_SECURITY_JS_CODE as string | undefined)?.trim() ?? "";
const amapStyle = (import.meta.env.VITE_AMAP_MAP_STYLE as string | undefined)?.trim() || "amap://styles/normal";
const autoFitCooldownMs = 60_000;
const routeRefreshCooldownMs = 8_000;
const routeRefreshMinMovementM = 80;
const driverAnimationDurationMs = 700;

let map: any = null;
let AMap: any = null;
let pickupMarker: any = null;
let destinationMarker: any = null;
let routeLine: any = null;
let driving: any = null;
let driverMarkers = new Map<string, any>();
let clickHandler: ((event: any) => void) | null = null;
let lastFitAt = 0;
let pendingFitTimer: number | null = null;
let pendingRouteTimer: number | null = null;
let lastRouteSearchAt = 0;
let routeSignature = "";
let lastRouteOrigin: [number, number] | null = null;
let activeRouteRequestId = 0;

const mergedDrivers = computed(() =>
  props.drivers.map((driver) => {
    const live = props.liveDriverLocations[driver.driver_id];
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

onMounted(async () => {
  if (!amapKey) {
    loadState.value = "error";
    errorMessage.value = "Missing AMap key.";
    emit("error", errorMessage.value);
    return;
  }

  try {
    loadState.value = "loading";
    if (amapSecurityJsCode) {
      (window as any)._AMapSecurityConfig = {
        securityJsCode: amapSecurityJsCode,
      };
    }

    AMap = await AMapLoader.load({
      key: amapKey,
      version: "2.0",
      plugins: ["AMap.Scale", "AMap.ToolBar", "AMap.Driving", "AMap.MoveAnimation"],
    });

    if (!mapHost.value) {
      throw new Error("Map host not found.");
    }

    map = new AMap.Map(mapHost.value, {
      viewMode: "3D",
      zoom: 13,
      center: [props.pickupLng, props.pickupLat],
      mapStyle: amapStyle,
      resizeEnable: true,
      pitch: 20,
    });

    map.addControl(new AMap.Scale());
    map.addControl(
      new AMap.ToolBar({
        position: {
          right: "12px",
          top: "16px",
        },
      }),
    );

    clickHandler = (event: any) => {
      emit("pick-location", {
        mode: props.pickMode,
        lat: event.lnglat.getLat(),
        lng: event.lnglat.getLng(),
      });
    };
    map.on("click", clickHandler);

    driving = new AMap.Driving({
      hideMarkers: true,
      autoFitView: false,
      showTraffic: false,
    });

    syncMarkers();
    fitView();
    loadState.value = "ready";
    emit("ready");
  } catch (error) {
    loadState.value = "error";
    errorMessage.value = error instanceof Error ? error.message : "Failed to load AMap.";
    emit("error", errorMessage.value);
  }
});

onBeforeUnmount(() => {
  if (clickHandler && map) {
    map.off("click", clickHandler);
  }
  if (pendingFitTimer !== null) {
    window.clearTimeout(pendingFitTimer);
    pendingFitTimer = null;
  }
  if (pendingRouteTimer !== null) {
    window.clearTimeout(pendingRouteTimer);
    pendingRouteTimer = null;
  }
  driving?.clear?.();
  driverMarkers.forEach((marker) => {
    marker?.stopMove?.();
    map?.remove?.(marker);
  });
  driverMarkers.clear();
  map?.destroy?.();
  map = null;
});

watch(
  () => [
    props.pickupLat,
    props.pickupLng,
    props.destinationLat,
    props.destinationLng,
    props.drivers,
    props.liveDriverLocations,
    props.currentOrder,
  ],
  () => {
    syncMarkers();
  },
  { deep: true },
);

function syncMarkers() {
  if (!map || !AMap) {
    return;
  }

  pickupMarker = upsertStaticMarker(pickupMarker, [props.pickupLng, props.pickupLat], badgeHTML("P", "#1f8f63"));
  destinationMarker = upsertStaticMarker(
    destinationMarker,
    [props.destinationLng, props.destinationLat],
    badgeHTML("D", "#2d6cdf"),
  );

  if (!routeLine) {
    routeLine = new AMap.Polyline({
      path: [],
      strokeColor: "#f26b3a",
      strokeWeight: 5,
      strokeOpacity: 0.88,
      lineJoin: "round",
      showDir: true,
    });
    map.add(routeLine);
  }

  routeLine.setPath([]);

  const activeIDs = new Set<string>();
  for (const driver of mergedDrivers.value) {
    activeIDs.add(driver.driver_id);
    const live = props.liveDriverLocations[driver.driver_id];
    const marker = upsertDriverMarker(
      driverMarkers.get(driver.driver_id) ?? null,
      [driver.location.lng, driver.location.lat],
      driverMarkerHTML(driver.driver_id.slice(-2)),
      live?.heading ?? 0,
    );
    driverMarkers.set(driver.driver_id, marker);
  }

  driverMarkers.forEach((marker, driverID) => {
    if (!activeIDs.has(driverID)) {
      marker?.stopMove?.();
      map.remove(marker);
      driverMarkers.delete(driverID);
    }
  });

  scheduleFitView();
  scheduleRouteRefresh();
}

function upsertStaticMarker(marker: any, position: [number, number], content: string) {
  if (!marker) {
    marker = new AMap.Marker({
      position,
      anchor: "bottom-center",
      content,
      zIndex: 120,
    });
    map.add(marker);
    return marker;
  }

  marker.setPosition(position);
  marker.setContent(content);
  return marker;
}

function upsertDriverMarker(marker: any, position: [number, number], content: string, heading = 0) {
  if (!marker) {
    marker = new AMap.Marker({
      position,
      anchor: "center",
      offset: new AMap.Pixel(0, 0),
      content,
      angle: heading,
      zIndex: 140,
    });
    map.add(marker);
    return marker;
  }

  marker.setContent(content);
  if (typeof marker.setAngle === "function") {
    marker.setAngle(heading);
  }
  moveDriverMarker(marker, position);
  return marker;
}

function moveDriverMarker(marker: any, position: [number, number]) {
  const current = readMarkerPosition(marker);
  if (!current || samePosition(current, position)) {
    marker.setPosition(position);
    return;
  }

  marker.stopMove?.();
  if (typeof marker.moveTo === "function") {
    marker.moveTo(position, {
      duration: driverAnimationDurationMs,
      autoRotation: true,
    });
    return;
  }

  marker.setPosition(position);
}

function readMarkerPosition(marker: any): [number, number] | null {
  const position = marker?.getPosition?.();
  const lng = position?.getLng?.();
  const lat = position?.getLat?.();
  if (typeof lng !== "number" || typeof lat !== "number") {
    return null;
  }
  return [lng, lat];
}

function samePosition(a: [number, number], b: [number, number]) {
  return Math.abs(a[0] - b[0]) < 0.000001 && Math.abs(a[1] - b[1]) < 0.000001;
}

function distanceMeters(a: [number, number], b: [number, number]) {
  const midLatRad = ((a[1] + b[1]) / 2) * Math.PI / 180;
  const lngMeters = (b[0] - a[0]) * 111320 * Math.cos(midLatRad);
  const latMeters = (b[1] - a[1]) * 111320;
  return Math.hypot(latMeters, lngMeters);
}

function fitView() {
  if (!map) {
    return;
  }

  const overlays = [pickupMarker, destinationMarker, routeLine, ...driverMarkers.values()].filter(Boolean);
  if (overlays.length > 0) {
    lastFitAt = Date.now();
    map.setFitView(overlays, false, [64, 48, 48, 48]);
  }
}

function scheduleFitView() {
  const now = Date.now();
  const remaining = autoFitCooldownMs - (now - lastFitAt);

  if (lastFitAt === 0 || remaining <= 0) {
    if (pendingFitTimer !== null) {
      window.clearTimeout(pendingFitTimer);
      pendingFitTimer = null;
    }
    fitView();
    return;
  }

  if (pendingFitTimer !== null) {
    return;
  }

  pendingFitTimer = window.setTimeout(() => {
    pendingFitTimer = null;
    fitView();
  }, remaining);
}

function scheduleRouteRefresh() {
  const plan = currentRoutePlan();
  if (!plan || !driving) {
    routeSignature = "";
    lastRouteOrigin = null;
    lastRouteSearchAt = 0;
    driving?.clear?.();
    setFallbackRoute([]);
    if (pendingRouteTimer !== null) {
      window.clearTimeout(pendingRouteTimer);
      pendingRouteTimer = null;
    }
    return;
  }

  const signature = `${plan.mode}:${plan.destination[0].toFixed(5)},${plan.destination[1].toFixed(5)}`;
  const movedEnough = !lastRouteOrigin || distanceMeters(lastRouteOrigin, plan.origin) >= routeRefreshMinMovementM;

  if (signature !== routeSignature || movedEnough) {
    routeSignature = signature;
    if (pendingRouteTimer !== null) {
      window.clearTimeout(pendingRouteTimer);
      pendingRouteTimer = null;
    }
    refreshDrivingRoute(plan.origin, plan.destination);
    return;
  }

  const remaining = routeRefreshCooldownMs - (Date.now() - lastRouteSearchAt);
  if (remaining <= 0) {
    refreshDrivingRoute(plan.origin, plan.destination);
    return;
  }
  if (pendingRouteTimer !== null) {
    return;
  }

  pendingRouteTimer = window.setTimeout(() => {
    pendingRouteTimer = null;
    const nextPlan = currentRoutePlan();
    if (!nextPlan) {
      driving?.clear?.();
      routeSignature = "";
      lastRouteOrigin = null;
      setFallbackRoute([]);
      return;
    }
    refreshDrivingRoute(nextPlan.origin, nextPlan.destination);
  }, remaining);
}

function refreshDrivingRoute(origin: [number, number], destination: [number, number]) {
  if (!driving) {
    return;
  }

  lastRouteSearchAt = Date.now();
  lastRouteOrigin = origin;
  const requestID = ++activeRouteRequestId;
  driving.search(origin, destination, (status: string, result: any) => {
    if (requestID !== activeRouteRequestId) {
      return;
    }
    if (status !== "complete") {
      console.warn("driving route search failed:", status);
      setFallbackRoute([origin, destination]);
      return;
    }
    const plannedPath = extractDrivingPath(result);
    if (plannedPath.length === 0) {
      setFallbackRoute([origin, destination]);
      return;
    }
    setFallbackRoute(plannedPath);
  });
}

function currentRoutePlan(): { origin: [number, number]; destination: [number, number]; mode: string } | null {
  const activePlan = activeDriverRoutePlan();
  if (activePlan) {
    return activePlan;
  }

  if (samePosition([props.pickupLng, props.pickupLat], [props.destinationLng, props.destinationLat])) {
    return null;
  }

  return {
    origin: [props.pickupLng, props.pickupLat],
    destination: [props.destinationLng, props.destinationLat],
    mode: "preview",
  };
}

function activeDriverRoutePlan(): { origin: [number, number]; destination: [number, number]; mode: string } | null {
  const order = props.currentOrder;
  if (!order || !order.driver_id) {
    return null;
  }

  const driver = props.liveDriverLocations[order.driver_id];
  if (!driver) {
    return null;
  }

  switch (order.status) {
    case "accepted":
    case "driver_arrived":
      return {
        origin: [driver.lng, driver.lat],
        destination: [props.pickupLng, props.pickupLat],
        mode: "pickup",
      };
    case "in_trip":
      return {
        origin: [driver.lng, driver.lat],
        destination: [props.destinationLng, props.destinationLat],
        mode: "trip",
      };
    default:
      return null;
  }
}

function setFallbackRoute(path: Array<[number, number]>) {
  routeLine?.setPath?.(path);
}

function extractDrivingPath(result: any): Array<[number, number]> {
  const steps = result?.routes?.[0]?.steps;
  if (!Array.isArray(steps)) {
    return [];
  }

  const path: Array<[number, number]> = [];
  for (const step of steps) {
    const points = step?.path;
    if (!Array.isArray(points)) {
      continue;
    }
    for (const point of points) {
      const lng = point?.lng ?? point?.getLng?.();
      const lat = point?.lat ?? point?.getLat?.();
      if (typeof lng !== "number" || typeof lat !== "number") {
        continue;
      }

      const next: [number, number] = [lng, lat];
      const prev = path[path.length - 1];
      if (!prev || !samePosition(prev, next)) {
        path.push(next);
      }
    }
  }
  return path;
}

function badgeHTML(text: string, color: string) {
  return `<div style="width:38px;height:38px;border-radius:999px;background:${color};color:#fff;display:flex;align-items:center;justify-content:center;font-weight:700;box-shadow:0 10px 24px rgba(0,0,0,.2);border:3px solid rgba(255,255,255,.9);">${text}</div>`;
}

function driverMarkerHTML(text: string) {
  return `<div style="position:relative;width:44px;height:44px;display:grid;place-items:center;">
    <div style="position:absolute;inset:8px;border-radius:18px;background:linear-gradient(135deg,#f26b3a,#ff9b56);box-shadow:0 12px 28px rgba(242,107,58,.28);border:2px solid rgba(255,255,255,.96);"></div>
    <div style="position:absolute;top:1px;left:50%;transform:translateX(-50%);width:0;height:0;border-left:8px solid transparent;border-right:8px solid transparent;border-bottom:12px solid #f26b3a;"></div>
    <div style="position:relative;color:#fff;font-size:12px;font-weight:800;letter-spacing:.03em;">${text}</div>
  </div>`;
}
</script>

<template>
  <div class="map-shell">
    <div ref="mapHost" class="map-host"></div>

    <div v-if="loadState !== 'ready'" class="map-overlay">
      <template v-if="loadState === 'loading'">
        <strong>Loading AMap...</strong>
        <p>Initializing JSAPI 2.0.</p>
      </template>
      <template v-else>
        <strong>Map unavailable</strong>
        <p>{{ errorMessage || "Check the AMap key in the frontend env file." }}</p>
        <code>frontend-passenger/.env.local</code>
      </template>
    </div>

    <div class="map-tip">
      Click the map to set {{ pickMode === "pickup" ? "pickup" : "destination" }}.
    </div>
  </div>
</template>

<style scoped>
.map-shell {
  position: relative;
  height: 420px;
  border-radius: 28px;
  overflow: hidden;
  border: 1px solid rgba(31, 41, 51, 0.08);
  background: linear-gradient(180deg, rgba(223, 236, 232, 0.7), rgba(244, 239, 228, 0.9));
}

.map-host {
  width: 100%;
  height: 100%;
}

.map-overlay {
  position: absolute;
  inset: 0;
  display: grid;
  place-content: center;
  gap: 8px;
  text-align: center;
  background: rgba(252, 246, 235, 0.86);
  color: #1f2933;
  backdrop-filter: blur(8px);
}

.map-overlay p,
.map-overlay code {
  margin: 0;
  color: #5e6c76;
}

.map-tip {
  position: absolute;
  left: 16px;
  bottom: 16px;
  padding: 10px 14px;
  border-radius: 999px;
  background: rgba(255, 249, 241, 0.9);
  color: #5e6c76;
  border: 1px solid rgba(31, 41, 51, 0.08);
  font-size: 13px;
}

@media (max-width: 720px) {
  .map-shell {
    height: 320px;
    border-radius: 20px;
  }
}
</style>
