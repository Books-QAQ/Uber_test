# Uber-Test 打车调度测试平台

这是一个使用 Go + Vue3 开发的打车调度项目，包含乘客端、司机调试端、自动派单、实时位置推送、路线规划和持久化存储。

目前主要能力：

- 乘客注册、登录、下单和订单状态查看
- 司机注册、登录、上线、手动接单和订单状态推进
- 司机位置上报、心跳检测和超时下线
- Redis GEO / R-tree 附近司机查询
- WebSocket 实时位置和订单事件推送
- 高德地图展示与路线规划
- OSRM 路线规划和地图匹配
- MySQL 业务数据、订单及行程轨迹持久化
- 独立的 `driver-sim` 自动司机测试程序

## Demo 演示

- [查看 Demo 视频](docs/demo.mp4)
- 演示内容：乘客下单、自动派单、司机接单、车辆行驶、接送乘客和地图实时更新

## 项目结构

```text
Uber_test/
├─ backend/             Go 后端、UDP 服务和司机模拟器
├─ frontend-passenger/  乘客端，默认端口 5173
├─ frontend-order/      司机调试端，默认端口 5174
├─ docs/                项目文档和 Demo
└─ docker-compose.yml   MySQL、Redis 开发环境
```

## 环境要求

- Go 1.22+
- Node.js 18+
- npm
- 可选：MySQL 8+
- 可选：Redis 7+
- 可选：Docker Desktop



前端地图需要申请，在 `.env.local` 中填写自己的高德配置：

```env
VITE_AMAP_KEY=你的高德Web端JSAPIKey
VITE_AMAP_SECURITY_JS_CODE=你的安全密钥
VITE_AMAP_MAP_STYLE=amap://styles/normal
```

后端使用高德 REST 算路时，还需要高德 Web 服务 Key。没有配置高德 Web 服务 Key 时，后端会尝试使用 OSRM。

## 一、启动后端

后端支持三种运行方式，按开发需求选择其中一种即可。

### 方式 1：直接运行（纯内存）

这是最快的启动方式，不需要 MySQL、Redis 或 Docker。服务重启后数据会清空。

```powershell
cd D:\golang学习\项目\Uber_test\backend
go run ./cmd/server
```

适合快速体验接口、前端联调和运行单元测试。

### 方式 2：使用本地 MySQL 和 Redis

先确保本机服务已经启动：

- MySQL：`127.0.0.1:3306`
- Redis：`127.0.0.1:6379`

创建数据库：

```sql
CREATE DATABASE IF NOT EXISTS uber_test
CHARACTER SET utf8mb4
COLLATE utf8mb4_unicode_ci;
```

然后使用开发脚本启动后端，并传入本机数据库密码：

```powershell
cd D:\golang学习\项目\Uber_test\backend

.\scripts\run-server-dev.ps1 `
  -MySQLDSN "root:你的密码@tcp(127.0.0.1:3306)/uber_test?parseTime=true" `
  -RedisAddr "127.0.0.1:6379"
```

如果 Redis 设置了密码：

```powershell
.\scripts\run-server-dev.ps1 `
  -MySQLDSN "root:你的密码@tcp(127.0.0.1:3306)/uber_test?parseTime=true" `
  -RedisAddr "127.0.0.1:6379" `
  -RedisPassword "你的Redis密码"
```

后端启动时会自动执行 `backend/internal/storage/mysql/migrations/` 下的 SQL 迁移。

只启用其中一种存储也可以：

```powershell
# 仅使用 MySQL，不使用 Redis
.\scripts\run-server-dev.ps1 `
  -MySQLDSN "root:你的密码@tcp(127.0.0.1:3306)/uber_test?parseTime=true" `
  -DisableRedis

# 仅使用 Redis，其他业务数据使用内存
.\scripts\run-server-dev.ps1 -DisableMySQL
```

### 方式 3：使用 Docker 启动 MySQL 和 Redis

当前 Docker Compose 负责运行 MySQL 和 Redis，Go 后端仍在本机启动。

确保 Docker Desktop 已经运行，然后在项目根目录执行：

```powershell
cd D:\golang学习\项目\Uber_test
docker compose up -d
docker compose ps
```

默认开发配置：

- MySQL：`root / uber_test_dev`
- 数据库：`uber_test`
- Redis：`127.0.0.1:6379`

再启动 Go 后端：

```powershell
cd backend
.\scripts\run-server-dev.ps1
```

停止容器：

```powershell
cd D:\golang学习\项目\Uber_test
docker compose down
```

如果本机 MySQL 已占用 `3306` 端口，需要先停止本机 MySQL，或者修改 `docker-compose.yml` 的端口映射。

如果 Docker 数据卷曾使用其他 MySQL 密码初始化，请在启动脚本中传入实际密码；修改 Compose 环境变量不会自动修改已有数据卷中的密码。

### 后端服务地址

后端默认提供：

- HTTP API：`http://127.0.0.1:8080`
- 健康检查：`http://127.0.0.1:8080/healthz`
- UDP + Protobuf 位置入口：`127.0.0.1:9000`
- WebSocket：`ws://127.0.0.1:8080/ws/location`

验证服务：

```powershell
Invoke-RestMethod http://127.0.0.1:8080/healthz
```

### 后端路线配置

`backend/configs/config.example.env` 是配置参考文件，程序不会自动读取它。可以通过 PowerShell 环境变量或启动脚本传入配置。

```powershell
$env:ROUTE_AMAP_WEB_KEY = "你的高德Web服务Key"
$env:ROUTE_OSRM_BASE_URL = "http://router.project-osrm.org"
$env:ROUTE_REQUEST_TIMEOUT = "5s"
$env:MAPMATCH_ENABLED = "true"
```

## 二、启动乘客端

乘客端目录为 `frontend-passenger/`，默认端口为 `5173`。

复制环境变量示例：

```powershell
cd D:\golang学习\项目\Uber_test\frontend-passenger
Copy-Item .env.example .env.local
```

安装依赖并启动：

```powershell
npm install
npm run dev
```

访问：

```text
http://localhost:5173
```

乘客端用于注册、登录、选择上下车点、创建订单、查看司机位置和订单进度。

## 三、启动司机调试端

司机端目录为 `frontend-order/`，默认端口为 `5174`。它与 `backend/cmd/driver-sim` 相互独立，适合手动调试完整司机流程。

填写高德配置：

```env
VITE_AMAP_KEY=你的高德Web端JSAPIKey
VITE_AMAP_SECURITY_JS_CODE=你的安全密钥
VITE_AMAP_MAP_STYLE=amap://styles/normal
```

安装依赖并启动：

```powershell
npm install
npm run dev
```

访问：

```text
http://localhost:5174
```

司机端支持：

- 注册和登录司机账号
- 设置车牌号和司机在线状态
- 查看待接派单并手动接单
- 手动确认到达上车点、乘客上车和送达
- 自动沿后端规划路线行驶
- 在“模拟行驶”和“GPS 定位”之间切换

GPS 定位注意事项：

- 本地开发使用 `http://localhost:5174` 可以申请浏览器定位权限
- 使用局域网 IP 或公网域名时需要 HTTPS
- GPS 模式会持续采集当前位置并上报后端
- 切换回模拟模式后，车辆继续使用自动行驶逻辑

## 四、可选：启动自动司机模拟器

如果不想手动操作司机端，可以运行独立的自动司机模拟器：

```powershell
cd D:\golang学习\项目\Uber_test\backend
go run ./cmd/driver-sim -drivers 2
```

模拟器会自动：

- 注册和登录测试司机
- 设置司机在线
- 通过 UDP + Protobuf 上报位置
- 接收并接受派单
- 沿道路路线移动
- 推进接客、行程和完成状态

司机调试端和 `driver-sim` 不建议使用同一个司机账号同时运行，否则两个位置源可能互相覆盖。

## 五、推荐联调顺序

1. 启动后端。
2. 启动乘客端 `http://localhost:5173`。
3. 启动司机端 `http://localhost:5174`，或运行 `driver-sim`。
4. 在司机端注册并上线。
5. 在乘客端注册、设置上车点和目的地并创建订单。
6. 在司机端接单并确认各阶段状态。
7. 观察两端地图、路线、车辆位置和订单状态是否同步。
