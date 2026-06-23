<script setup lang="ts">
import AMapLoader from "@amap/amap-jsapi-loader";
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

import type { DriverLiveLocation, DriverRoute, NearbyDriver, Order } from "../types";

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
  actualRoute: DriverRoute | null;
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
const driverAnimationDurationMs = 700;
const routeDeviationRefreshM = 20;
const routeDeviationCooldownMs = 4_000;

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
let routeSignature = "";
let activeRouteRequestId = 0;
let plannedRoutePath: Array<[number, number]> = [];
let consumedRouteIndex = 0;
let lastRouteRefreshAt = 0;

type RoutePlan = {
  origin: [number, number];
  destination: [number, number];
  mode: "preview" | "pickup" | "trip";
  signature: string;
  driverPosition?: [number, number];
};

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
      policy: AMap?.DrivingPolicy?.LEAST_TIME,
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
    props.actualRoute,
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
  syncRouteLine();
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

function syncRouteLine() {
  const plan = currentRoutePlan();
  if (!plan || !driving) {
    clearRouteState();
    return;
  }

  const actualPath = currentActualRoutePath(plan);
  if (actualPath) {
    const actualSignature = `actual:${props.actualRoute?.order_id ?? ""}:${props.actualRoute?.mode ?? ""}:${props.actualRoute?.updated_at ?? ""}`;
    if (routeSignature !== actualSignature) {
      routeSignature = actualSignature;
      plannedRoutePath = dedupePath(actualPath);
      consumedRouteIndex = 0;
      lastRouteRefreshAt = Date.now();
      activeRouteRequestId += 1;
    }
    renderRoutePath(plan, plannedRoutePath);
    return;
  }

  if (shouldRefreshRouteForDeviation(plan)) {
    refreshDrivingRoute(plan);
    if (plannedRoutePath.length === 0) {
      renderRoutePath(plan, [plan.origin, plan.destination]);
    }
    return;
  }

  if (plan.signature !== routeSignature || plannedRoutePath.length === 0) {
    refreshDrivingRoute(plan);
    if (plannedRoutePath.length === 0) {
      renderRoutePath(plan, [plan.origin, plan.destination]);
    }
    return;
  }

  renderRoutePath(plan, plannedRoutePath);
}

function clearRouteState() {
  routeSignature = "";
  plannedRoutePath = [];
  consumedRouteIndex = 0;
  lastRouteRefreshAt = 0;
  activeRouteRequestId += 1;
  driving?.clear?.();
  setFallbackRoute([]);
}

function refreshDrivingRoute(plan: RoutePlan) {
  if (!driving) {
    return;
  }

  routeSignature = plan.signature;
  plannedRoutePath = [];
  consumedRouteIndex = 0;
  lastRouteRefreshAt = Date.now();
  const requestID = ++activeRouteRequestId;
  driving.search(plan.origin, plan.destination, (status: string, result: any) => {
    if (requestID !== activeRouteRequestId) {
      return;
    }
    if (status !== "complete") {
      console.warn("driving route search failed:", status);
      plannedRoutePath = dedupePath([plan.origin, plan.destination]);
      renderRoutePath(plan, plannedRoutePath);
      return;
    }
    const plannedPath = extractDrivingPath(result);
    plannedRoutePath = dedupePath(plannedPath.length > 0 ? plannedPath : [plan.origin, plan.destination]);

    const latestPlan = currentRoutePlan();
    if (!latestPlan || latestPlan.signature !== plan.signature) {
      return;
    }
    renderRoutePath(latestPlan, plannedRoutePath);
  });
}

function renderRoutePath(plan: RoutePlan, path: Array<[number, number]>) {
  const normalized = dedupePath(path);
  if (normalized.length === 0) {
    setFallbackRoute([]);
    return;
  }

  if (plan.mode === "preview" || !plan.driverPosition) {
    setFallbackRoute(normalized);
    return;
  }

  setFallbackRoute(trimDrivenPath(normalized, plan.driverPosition));
}

function currentActualRoutePath(plan: RoutePlan) {
  const route = props.actualRoute;
  const order = props.currentOrder;
  if (!route || !order || !order.driver_id || plan.mode === "preview") {
    return null;
  }
  if (route.order_id !== order.id || route.driver_id !== order.driver_id) {
    return null;
  }
  if (route.mode && route.mode !== plan.mode) {
    return null;
  }

  const points = route.points
    .map((point) => [point.lng, point.lat] as [number, number])
    .filter((point) => Number.isFinite(point[0]) && Number.isFinite(point[1]));
  return points.length > 0 ? dedupePath(points) : null;
}

function shouldRefreshRouteForDeviation(plan: RoutePlan) {
  if (plan.mode === "preview" || !plan.driverPosition || plannedRoutePath.length < 2) {
    return false;
  }
  if (plan.signature !== routeSignature) {
    return false;
  }
  if (Date.now() - lastRouteRefreshAt < routeDeviationCooldownMs) {
    return false;
  }
  return distanceToPathMeters(plan.driverPosition, plannedRoutePath) > routeDeviationRefreshM;
}

function trimDrivenPath(path: Array<[number, number]>, driverPosition: [number, number]) {
  if (path.length === 0) {
    return [driverPosition];
  }

  if (path.length === 1) {
    return dedupePath([driverPosition, path[0]]);
  }

  const startIndex = Math.max(0, Math.min(consumedRouteIndex, path.length - 2));
  let nearestSegmentIndex = startIndex;
  let nearestProjection = path[startIndex];
  let nearestDistance = Number.POSITIVE_INFINITY;

  for (let index = startIndex; index < path.length - 1; index += 1) {
    const projection = projectPointToSegment(driverPosition, path[index], path[index + 1]);
    if (projection.distance < nearestDistance) {
      nearestDistance = projection.distance;
      nearestSegmentIndex = index;
      nearestProjection = projection.point;
    }
  }

  consumedRouteIndex = Math.max(consumedRouteIndex, nearestSegmentIndex);
  const visiblePath: Array<[number, number]> = [driverPosition];
  if (!samePosition(visiblePath[visiblePath.length - 1], nearestProjection)) {
    visiblePath.push(nearestProjection);
  }
  for (const point of path.slice(consumedRouteIndex + 1)) {
    if (!samePosition(visiblePath[visiblePath.length - 1], point)) {
      visiblePath.push(point);
    }
  }
  return visiblePath;
}

function projectPointToSegment(point: [number, number], start: [number, number], end: [number, number]) {
  const midLatRad = ((start[1] + end[1] + point[1]) / 3) * Math.PI / 180;
  const scaleX = 111320 * Math.cos(midLatRad);
  const scaleY = 111320;

  const sx = start[0] * scaleX;
  const sy = start[1] * scaleY;
  const ex = end[0] * scaleX;
  const ey = end[1] * scaleY;
  const px = point[0] * scaleX;
  const py = point[1] * scaleY;

  const dx = ex - sx;
  const dy = ey - sy;
  const lengthSquared = dx * dx + dy * dy;
  if (lengthSquared === 0) {
    return {
      point: start,
      distance: distanceMeters(point, start),
    };
  }

  const ratio = Math.max(0, Math.min(1, ((px-sx)*dx + (py-sy)*dy) / lengthSquared));
  const projected: [number, number] = [
    start[0] + (end[0] - start[0]) * ratio,
    start[1] + (end[1] - start[1]) * ratio,
  ];
  return {
    point: projected,
    distance: distanceMeters(point, projected),
  };
}

function distanceToPathMeters(point: [number, number], path: Array<[number, number]>) {
  if (path.length === 0) {
    return Number.POSITIVE_INFINITY;
  }
  if (path.length === 1) {
    return distanceMeters(point, path[0]);
  }

  let best = Number.POSITIVE_INFINITY;
  for (let index = 0; index < path.length - 1; index += 1) {
    const projection = projectPointToSegment(point, path[index], path[index + 1]);
    if (projection.distance < best) {
      best = projection.distance;
    }
  }
  return best;
}

function dedupePath(path: Array<[number, number]>) {
  const normalized: Array<[number, number]> = [];
  for (const point of path) {
    const prev = normalized[normalized.length - 1];
    if (!prev || !samePosition(prev, point)) {
      normalized.push(point);
    }
  }
  return normalized;
}

function currentRoutePlan(): RoutePlan | null {
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
    signature: `preview:${props.pickupLng.toFixed(5)},${props.pickupLat.toFixed(5)}:${props.destinationLng.toFixed(5)},${props.destinationLat.toFixed(5)}`,
  };
}

function activeDriverRoutePlan(): RoutePlan | null {
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
        signature: `pickup:${order.id}:${order.driver_id}:${props.pickupLng.toFixed(5)},${props.pickupLat.toFixed(5)}`,
        driverPosition: [driver.lng, driver.lat],
      };
    case "in_trip":
      return {
        origin: [driver.lng, driver.lat],
        destination: [props.destinationLng, props.destinationLat],
        mode: "trip",
        signature: `trip:${order.id}:${order.driver_id}:${props.destinationLng.toFixed(5)},${props.destinationLat.toFixed(5)}`,
        driverPosition: [driver.lng, driver.lat],
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
  const carSVG = encodeURIComponent(`
    <svg xmlns="http://www.w3.org/2000/svg" width="64" height="64" viewBox="0 0 64 64">
      <defs>
        <linearGradient id="carBody" x1="0%" y1="0%" x2="100%" y2="100%">
          <stop offset="0%" stop-color="#ff9f5a"/>
          <stop offset="100%" stop-color="#e45528"/>
        </linearGradient>
      </defs>
      <g>
        <ellipse cx="32" cy="56" rx="15" ry="4" fill="rgba(58,30,14,0.18)"/>
        <rect x="18" y="10" width="28" height="44" rx="12" fill="url(#carBody)" stroke="#fff8ef" stroke-width="3"/>
        <path d="M24 19c0-3.3 2.7-6 6-6h4c3.3 0 6 2.7 6 6v9H24z" fill="#ffe6cf"/>
        <rect x="24" y="33" width="16" height="13" rx="5" fill="#fff4ea" opacity="0.96"/>
        <rect x="21" y="15" width="4" height="10" rx="2" fill="#ffd1b1" opacity="0.9"/>
        <rect x="39" y="15" width="4" height="10" rx="2" fill="#ffd1b1" opacity="0.9"/>
        <rect x="21" y="39" width="4" height="10" rx="2" fill="#bf3f1b" opacity="0.92"/>
        <rect x="39" y="39" width="4" height="10" rx="2" fill="#bf3f1b" opacity="0.92"/>
        <circle cx="23" cy="22" r="3.5" fill="#10151c"/>
        <circle cx="41" cy="22" r="3.5" fill="#10151c"/>
        <circle cx="23" cy="43" r="3.5" fill="#10151c"/>
        <circle cx="41" cy="43" r="3.5" fill="#10151c"/>
      </g>
    </svg>
  `);

  return `<div style="position:relative;width:52px;height:52px;display:grid;place-items:center;filter:drop-shadow(0 12px 18px rgba(83,45,17,.22));">
    <img src="data:image/svg+xml;charset=UTF-8,${carSVG}" alt="car" style="width:52px;height:52px;display:block;user-select:none;pointer-events:none;" />
    <div style="position:absolute;right:-4px;bottom:-1px;min-width:20px;height:20px;padding:0 6px;border-radius:999px;background:rgba(31,41,51,.88);color:#fff;font-size:10px;font-weight:800;line-height:20px;text-align:center;border:2px solid rgba(255,249,241,.96);">${text}</div>
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
