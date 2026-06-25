export type Role = "passenger" | "driver" | "admin";

export type User = {
  id: string;
  phone: string;
  role: Role;
  display_name?: string;
  driver_id?: string;
};

export type LoginResult = {
  token: string;
  user: User;
};

export type Order = {
  id: string;
  passenger_id: string;
  driver_id?: string;
  driver_plate_no?: string;
  status: string;
  pickup_lat: number;
  pickup_lng: number;
  pickup_address?: string;
  destination_lat: number;
  destination_lng: number;
  destination_address?: string;
  estimated_price?: number;
  final_price?: number;
  created_at: string;
  updated_at: string;
};

export type DispatchRecord = {
  id: string;
  order_id: string;
  driver_id: string;
  status: string;
  distance_m: number;
  dispatch_round: number;
  created_at: string;
  updated_at: string;
};

export type DispatchAssignment = {
  dispatch: DispatchRecord;
  order: Order;
};

export type DriverLocation = {
  driver_id: string;
  order_id?: string;
  lat: number;
  lng: number;
  speed_kph?: number;
  heading?: number;
  accuracy_m?: number;
  timestamp?: string;
};

export type DriverProfile = {
  user_id: string;
  driver_id: string;
  display_name?: string;
  phone: string;
  plate_no?: string;
};

export type RoutePoint = {
  lat: number;
  lng: number;
};

export type DriverRoute = {
  driver_id: string;
  order_id: string;
  mode: string;
  points: RoutePoint[];
  updated_at: string;
};
