import type { RoutePoint } from "./types";

export function distanceMeters(fromLat: number, fromLng: number, toLat: number, toLng: number) {
  const midLatRad = ((fromLat + toLat) / 2) * Math.PI / 180;
  const lngMeters = (toLng - fromLng) * 111320 * Math.cos(midLatRad);
  const latMeters = (toLat - fromLat) * 111320;
  return Math.hypot(latMeters, lngMeters);
}

export function bearingDegrees(fromLat: number, fromLng: number, toLat: number, toLng: number) {
  const latMeters = (toLat - fromLat) * 111320;
  const midLatRad = ((fromLat + toLat) / 2) * Math.PI / 180;
  const lngMeters = (toLng - fromLng) * 111320 * Math.cos(midLatRad);
  if (latMeters === 0 && lngMeters === 0) {
    return 0;
  }
  let heading = Math.atan2(lngMeters, latMeters) * 180 / Math.PI;
  if (heading < 0) {
    heading += 360;
  }
  return heading;
}

export function moveTowards(fromLat: number, fromLng: number, toLat: number, toLng: number, stepM: number) {
  const distance = distanceMeters(fromLat, fromLng, toLat, toLng);
  if (distance === 0 || distance <= stepM) {
    return {
      lat: toLat,
      lng: toLng,
      traveledM: distance,
    };
  }

  const ratio = stepM / distance;
  return {
    lat: fromLat + (toLat - fromLat) * ratio,
    lng: fromLng + (toLng - fromLng) * ratio,
    traveledM: stepM,
  };
}

export function followPath(currentLat: number, currentLng: number, path: RoutePoint[], stepM: number) {
  let lat = currentLat;
  let lng = currentLng;
  let remaining = stepM;
  let traveledM = 0;
  const nextPath = [...path];

  while (remaining > 0 && nextPath.length > 0) {
    const nextPoint = nextPath[0];
    const distance = distanceMeters(lat, lng, nextPoint.lat, nextPoint.lng);
    if (distance === 0) {
      nextPath.shift();
      continue;
    }

    if (distance <= remaining) {
      lat = nextPoint.lat;
      lng = nextPoint.lng;
      remaining -= distance;
      traveledM += distance;
      nextPath.shift();
      continue;
    }

    const moved = moveTowards(lat, lng, nextPoint.lat, nextPoint.lng, remaining);
    lat = moved.lat;
    lng = moved.lng;
    traveledM += moved.traveledM;
    remaining = 0;
  }

  return {
    lat,
    lng,
    traveledM,
    path: nextPath,
  };
}

export function clampStepMeters(speedKph: number, tickMs: number) {
  if (speedKph <= 0 || tickMs <= 0) {
    return 1;
  }
  return speedKph * 1000 / 3600 * (tickMs / 1000);
}

export function metersToLat(meters: number) {
  return meters / 111320;
}

export function metersToLng(meters: number, lat: number) {
  return meters / (111320 * Math.cos(lat * Math.PI / 180));
}

export function samePoint(a: RoutePoint | null | undefined, b: RoutePoint | null | undefined) {
  if (!a || !b) {
    return false;
  }
  return Math.abs(a.lat - b.lat) < 0.000001 && Math.abs(a.lng - b.lng) < 0.000001;
}

export function trimPathFromCurrent(path: RoutePoint[], current: RoutePoint) {
  const normalized = dedupePath(path);
  if (normalized.length === 0) {
    return [];
  }
  if (normalized.length === 1) {
    return samePoint(current, normalized[0]) ? normalized : [current, normalized[0]];
  }

  let nearestSegmentIndex = 0;
  let nearestProjection = normalized[0];
  let nearestDistance = Number.MAX_SAFE_INTEGER;

  for (let index = 0; index < normalized.length - 1; index += 1) {
    const projection = projectPointToSegment(current, normalized[index], normalized[index + 1]);
    if (projection.distance < nearestDistance) {
      nearestDistance = projection.distance;
      nearestSegmentIndex = index;
      nearestProjection = projection.point;
    }
  }

  const trimmed: RoutePoint[] = [current];
  if (!samePoint(trimmed[trimmed.length - 1], nearestProjection)) {
    trimmed.push(nearestProjection);
  }

  for (const point of normalized.slice(nearestSegmentIndex + 1)) {
    if (!samePoint(trimmed[trimmed.length - 1], point)) {
      trimmed.push(point);
    }
  }

  return dedupePath(trimmed);
}

export function dedupePath(path: RoutePoint[]) {
  const result: RoutePoint[] = [];
  for (const point of path) {
    const last = result[result.length - 1];
    if (!last || distanceMeters(last.lat, last.lng, point.lat, point.lng) > 1) {
      result.push(point);
    }
  }
  return result;
}

function projectPointToSegment(point: RoutePoint, start: RoutePoint, end: RoutePoint) {
  const midLatRad = ((start.lat + end.lat + point.lat) / 3) * Math.PI / 180;
  const scaleX = 111320 * Math.cos(midLatRad);
  const scaleY = 111320;

  const sx = start.lng * scaleX;
  const sy = start.lat * scaleY;
  const ex = end.lng * scaleX;
  const ey = end.lat * scaleY;
  const px = point.lng * scaleX;
  const py = point.lat * scaleY;

  const dx = ex - sx;
  const dy = ey - sy;
  const lengthSquared = dx * dx + dy * dy;
  if (lengthSquared === 0) {
    return {
      point: start,
      distance: distanceMeters(point.lat, point.lng, start.lat, start.lng),
    };
  }

  const ratio = Math.max(0, Math.min(1, ((px - sx) * dx + (py - sy) * dy) / lengthSquared));
  const projected = {
    lat: start.lat + (end.lat - start.lat) * ratio,
    lng: start.lng + (end.lng - start.lng) * ratio,
  };
  return {
    point: projected,
    distance: distanceMeters(point.lat, point.lng, projected.lat, projected.lng),
  };
}
