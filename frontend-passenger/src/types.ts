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

export type NearbyDriver = {
  driver_id: string;
  status: string;
  distance_m: number;
  location: {
    lat: number;
    lng: number;
    timestamp: string;
  };
};

export type Order = {
  id: string;
  passenger_id: string;
  driver_id?: string;
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

export type Trip = {
  id: string;
  order_id: string;
  status: string;
  actual_distance_m: number;
  actual_duration_s: number;
  waiting_duration_s: number;
  estimated_price: number;
  final_price: number;
};

export type SocketEvent = {
  type?: string;
  data?: unknown;
  count?: number;
  order_id?: string;
  driver_id?: string;
  status?: string;
  at?: string;
};

export type DriverLiveLocation = {
  lat: number;
  lng: number;
  heading?: number;
  timestamp?: string;
  order_id?: string;
};
