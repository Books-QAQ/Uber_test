# Uber_test Guideline

这是一个使用 `Go + Vue` 搭建的打车调度测试项目，当前已经具备下面这些核心链路：

- 乘客端前端页面
- Go 后端 API 服务
- UDP + Protobuf 司机位置上报
- WebSocket 实时位置推送
- 自动派单
- 司机模拟器
- 可选 Redis / MySQL 持久化

## Demo 演示

- 演示视频：[docs/demo.mp4](docs/demo.mp4)
- 内容：乘客端下单、自动派单、司机模拟行驶、地图实时更新


## 1. 运行前准备

建议本机先安装：

- Go 1.22 或更高版本
- Node.js 18 或更高版本
- npm
- 可选：Redis 7+
- 可选：MySQL 8+

高德地图相关：

- 乘客端前端使用高德 JSAPI
- 司机模拟器如果想按真实道路规划行驶，建议准备高德 Web 服务 Key

## 2. 启动后端

在项目根目录打开终端：

```powershell
cd backend
go run ./cmd/server
```

启动成功后，后端默认提供：

- HTTP API: `http://127.0.0.1:8080`
- 健康检查: `http://127.0.0.1:8080/healthz`
- UDP 位置上报端口: `127.0.0.1:9000`
- WebSocket: `ws://127.0.0.1:8080/ws/location`

建议先访问一次健康检查确认服务已起来：

```powershell
curl http://127.0.0.1:8080/healthz
```

## 3. 启动乘客端前端

前端目录：

- `frontend-passenger/`

先配置高德地图环境变量。新建文件：

- `frontend-passenger/.env.local`

内容示例：

```env
VITE_AMAP_KEY=你的高德Web端JSAPIKey
VITE_AMAP_SECURITY_JS_CODE=你的安全密钥
VITE_AMAP_MAP_STYLE=amap://styles/normal
```

然后安装依赖并启动：

```powershell
cd frontend-passenger
npm install
npm run dev
```

默认访问地址：

- `http://127.0.0.1:5173`


## 4. 启动司机模拟器

这个项目当前没有单独的司机前端，司机侧主要通过模拟器来跑链路。

启动方式：

```powershell
cd backend
go run ./cmd/driver-sim -drivers 2
```

默认行为：

- 自动注册司机账号
- 自动登录司机账号
- 自动将司机设置为在线
- 周期性上报 UDP 位置
- 轮询派单列表
- 自动接单
- 自动推进订单状态直到完成

常用参数示例：

```powershell
cd backend
go run ./cmd/driver-sim `
  -drivers 3 `
  -lat 31.2304 `
  -lng 121.4737 `
  -radius-m 700 `
  -amap-web-key 你的高德Web服务Key
```

说明：

- `-drivers`：模拟司机数量
- `-lat` / `-lng`：司机活动中心点
- `-radius-m`：空闲绕行半径
- `-amap-web-key`：用于按道路路径模拟行驶

如果不传 `-amap-web-key`，模拟器会尝试读取前端 `.env.local` 或环境变量中的高德 Key；仍然拿不到时，会退回 OSRM。

## 5. 推荐的完整启动顺序

建议按这个顺序跑：

1. 启动后端
2. 启动前端
3. 启动司机模拟器
4. 打开乘客端页面注册/登录
5. 在地图上设置上车点和目的地
6. 创建订单
7. 观察自动派单、司机移动、WebSocket 实时更新

