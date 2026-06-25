<script setup lang="ts">
import AMapLoader from "@amap/amap-jsapi-loader";
import { onBeforeUnmount, onMounted, ref, watch } from "vue";

import type { DriverLocation, DriverRoute } from "../types";

const props = defineProps<{
  driverLocation: DriverLocation | null;
  routeData: DriverRoute | null;
  pickupLat: number | null;
  pickupLng: number | null;
  destinationLat: number | null;
  destinationLng: number | null;
}>();

const emit = defineEmits<{
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

let map: any = null;
let AMap: any = null;
let driverMarker: any = null;
let pickupMarker: any = null;
let destinationMarker: any = null;
let routeLine: any = null;
let lastFitAt = 0;
let pendingFitTimer: number | null = null;

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
      zoom: 14,
      center: props.driverLocation ? [props.driverLocation.lng, props.driverLocation.lat] : [121.4737, 31.2304],
      mapStyle: amapStyle,
      resizeEnable: true,
      pitch: 20,
    });

    map.addControl(new AMap.Scale());
    map.addControl(new AMap.ToolBar());
    syncMap(true);
    loadState.value = "ready";
    emit("ready");
  } catch (error) {
    loadState.value = "error";
    errorMessage.value = error instanceof Error ? error.message : "Failed to load AMap.";
    emit("error", errorMessage.value);
  }
});

onBeforeUnmount(() => {
  if (pendingFitTimer !== null) {
    window.clearTimeout(pendingFitTimer);
    pendingFitTimer = null;
  }
  map?.destroy?.();
  map = null;
});

watch(
  () => props.driverLocation,
  () => {
    syncMap(false);
  },
  { deep: true },
);

watch(
  () => [props.routeData, props.pickupLat, props.pickupLng, props.destinationLat, props.destinationLng],
  () => {
    syncMap(true);
  },
  { deep: true },
);

function syncMap(shouldFit: boolean) {
  if (!map || !AMap) {
    return;
  }

  if (props.driverLocation) {
    driverMarker = upsertMarker(
      driverMarker,
      [props.driverLocation.lng, props.driverLocation.lat],
      driverMarkerHTML(),
      props.driverLocation.heading ?? 0,
    );
  }

  if (props.pickupLat !== null && props.pickupLng !== null) {
    pickupMarker = upsertStaticMarker(pickupMarker, [props.pickupLng, props.pickupLat], badgeHTML("P", "#1f8f63"));
  }

  if (props.destinationLat !== null && props.destinationLng !== null) {
    destinationMarker = upsertStaticMarker(
      destinationMarker,
      [props.destinationLng, props.destinationLat],
      badgeHTML("D", "#2d6cdf"),
    );
  }

  if (!routeLine) {
    routeLine = new AMap.Polyline({
      path: [],
      strokeColor: "#f26b3a",
      strokeWeight: 6,
      strokeOpacity: 0.88,
      lineJoin: "round",
      showDir: true,
    });
    map.add(routeLine);
  }

  const visibleRoutePath = props.routeData?.mode === "idle"
    ? []
    : (props.routeData?.points.map((point) => [point.lng, point.lat]) ?? []);
  routeLine.setPath(visibleRoutePath);

  if (shouldFit) {
    scheduleFitView(visibleRoutePath.length > 0);
  }
}

function scheduleFitView(includeRouteLine: boolean) {
  const now = Date.now();
  const remaining = autoFitCooldownMs - (now - lastFitAt);

  if (lastFitAt === 0 || remaining <= 0) {
    fitView(includeRouteLine);
    return;
  }

  if (pendingFitTimer !== null) {
    return;
  }

  pendingFitTimer = window.setTimeout(() => {
    pendingFitTimer = null;
    fitView(includeRouteLine);
  }, remaining);
}

function fitView(includeRouteLine: boolean) {
  if (!map) {
    return;
  }

  const overlays = [
    driverMarker,
    pickupMarker,
    destinationMarker,
    includeRouteLine ? routeLine : null,
  ].filter(Boolean);

  if (overlays.length === 0) {
    return;
  }

  lastFitAt = Date.now();
  map.setFitView(overlays, false, [64, 48, 48, 48]);
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

function upsertMarker(marker: any, position: [number, number], content: string, heading = 0) {
  if (!marker) {
    marker = new AMap.Marker({
      position,
      anchor: "center",
      content,
      angle: heading,
      zIndex: 140,
    });
    map.add(marker);
    return marker;
  }

  marker.setContent(content);
  marker.setAngle?.(heading);
  marker.stopMove?.();
  if (typeof marker.moveTo === "function") {
    marker.moveTo(position, {
      duration: 500,
      autoRotation: true,
    });
  } else {
    marker.setPosition(position);
  }
  return marker;
}

function badgeHTML(text: string, color: string) {
  return `<div style="width:38px;height:38px;border-radius:999px;background:${color};color:#fff;display:flex;align-items:center;justify-content:center;font-weight:700;box-shadow:0 10px 24px rgba(0,0,0,.2);border:3px solid rgba(255,255,255,.9);">${text}</div>`;
}

function driverMarkerHTML() {
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
        <circle cx="23" cy="22" r="3.5" fill="#10151c"/>
        <circle cx="41" cy="22" r="3.5" fill="#10151c"/>
        <circle cx="23" cy="43" r="3.5" fill="#10151c"/>
        <circle cx="41" cy="43" r="3.5" fill="#10151c"/>
      </g>
    </svg>
  `);

  return `<div style="width:52px;height:52px;display:grid;place-items:center;filter:drop-shadow(0 12px 18px rgba(83,45,17,.22));">
    <img src="data:image/svg+xml;charset=UTF-8,${carSVG}" alt="car" style="width:52px;height:52px;display:block;user-select:none;pointer-events:none;" />
  </div>`;
}
</script>

<template>
  <div class="map-shell">
    <div ref="mapHost" class="map-host"></div>

    <div v-if="loadState !== 'ready'" class="map-overlay">
      <template v-if="loadState === 'loading'">
        <strong>Loading AMap...</strong>
        <p>Initializing driver debug map.</p>
      </template>
      <template v-else>
        <strong>Map unavailable</strong>
        <p>{{ errorMessage || "Check the AMap key in the env file." }}</p>
        <code>frontend-order/.env.local</code>
      </template>
    </div>
  </div>
</template>

<style scoped>
.map-shell {
  position: relative;
  height: 460px;
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
</style>
