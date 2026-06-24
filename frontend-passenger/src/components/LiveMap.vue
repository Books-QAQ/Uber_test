<script setup lang="ts">
import AMapLoader from "@amap/amap-jsapi-loader";
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

import type { DriverLiveLocation, DriverRoute, NearbyDriver } from "../types";

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
  routeData: DriverRoute | null;
  pickMode: "pickup" | "destination";
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

let map: any = null;
let AMap: any = null;
let pickupMarker: any = null;
let destinationMarker: any = null;
let routeLine: any = null;
let driverMarkers = new Map<string, any>();
let clickHandler: ((event: any) => void) | null = null;
let lastFitAt = 0;
let pendingFitTimer: number | null = null;

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
      plugins: ["AMap.Scale", "AMap.ToolBar", "AMap.MoveAnimation"],
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
    props.routeData,
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

  routeLine.setPath(
    props.routeData?.points?.map((point) => [point.lng, point.lat])?.filter((point) => point.length === 2) ?? [],
  );

  scheduleFitView();
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
