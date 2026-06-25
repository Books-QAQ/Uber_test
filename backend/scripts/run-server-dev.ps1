param(
    [string]$HTTPAddr = ":8080",
    [string]$UDPAddr = ":9000",
    [string]$MySQLDSN = "root:uber_test_dev@tcp(127.0.0.1:3306)/uber_test?parseTime=true",
    [string]$RedisAddr = "127.0.0.1:6379",
    [string]$RedisPassword = "",
    [string]$RedisDB = "0",
    [string]$RedisKeyPrefix = "uber-test",
    [string]$RouteOSRMBaseURL = "http://router.project-osrm.org",
    [string]$RouteRequestTimeout = "5s",
    [string]$MapMatchEnabled = "true",
    [switch]$DisableMySQL,
    [switch]$DisableRedis
)

$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$backendDir = Split-Path -Parent $scriptDir

Push-Location $backendDir
try {
    $env:APP_ENV = "local"
    $env:LOG_LEVEL = "info"
    $env:HTTP_ADDR = $HTTPAddr
    $env:UDP_ADDR = $UDPAddr

    $env:MYSQL_ENABLED = $(if ($DisableMySQL) { "false" } else { "true" })
    $env:MYSQL_DSN = $MySQLDSN
    $env:MYSQL_MAX_OPEN_CONNS = "10"
    $env:MYSQL_MAX_IDLE_CONNS = "5"
    $env:MYSQL_CONN_MAX_LIFETIME = "1h"

    $env:REDIS_ENABLED = $(if ($DisableRedis) { "false" } else { "true" })
    $env:REDIS_ADDR = $RedisAddr
    $env:REDIS_PASSWORD = $RedisPassword
    $env:REDIS_DB = $RedisDB
    $env:REDIS_KEY_PREFIX = $RedisKeyPrefix
    $env:REDIS_LOCATION_TTL = "24h"
    $env:REDIS_DISPATCH_TTL = "30m"

    $env:ROUTE_OSRM_BASE_URL = $RouteOSRMBaseURL
    $env:ROUTE_REQUEST_TIMEOUT = $RouteRequestTimeout
    $env:MAPMATCH_ENABLED = $MapMatchEnabled

    Write-Host "Starting backend server with dev infra settings..." -ForegroundColor Cyan
    Write-Host "MYSQL_ENABLED=$($env:MYSQL_ENABLED) MYSQL_DSN=$($env:MYSQL_DSN)" -ForegroundColor DarkGray
    Write-Host "REDIS_ENABLED=$($env:REDIS_ENABLED) REDIS_ADDR=$($env:REDIS_ADDR)" -ForegroundColor DarkGray

    go run ./cmd/server
}
finally {
    Pop-Location
}
