# easy-temp-cloud

临时文件托管服务，支持网页和客户端上传。

## 快速部署（Docker，推荐）

最快的方式是使用 Docker Compose 一键拉起。

1. 准备配置文件：

   ```powershell
   Copy-Item .env.example .env
   ```

   Linux / macOS：

   ```bash
   cp .env.example .env
   ```

2. 按需编辑 `.env`，将 `PUBLIC_BASE_URL` 改为外部可访问的地址，并设置 6-16 位的 `AUTH_PASSWORD`（详见下方[环境变量说明](#环境变量说明)）。

3. 构建镜像并后台启动：

   ```bash
   docker compose build
   docker compose up -d
   ```

4. 查看日志确认运行状态：

   ```bash
   docker compose logs -f
   ```

5. 网页上传入口为 `http://你的服务器地址:8080/`，需输入 `AUTH_PASSWORD` 登录。客户端上传地址如下，`pwd` 替换为你设置的 `AUTH_PASSWORD`：

   ```text
   # 客户端与服务不在同一台机器
   http://你的服务器地址:8080/api/upload?pwd=你的AUTH_PASSWORD

   # 客户端与服务在同一台机器
   http://localhost:8080/api/upload?pwd=你的AUTH_PASSWORD
   ```

   网页大文件上传使用标准 tus 端点 `/api/uploads/`。

> 默认端口映射为 `8080:8080`，可通过环境变量 `HOST_PORT` 修改宿主机侧端口（如 `HOST_PORT=9090` 映射为 `9090:8080`），容器内仍监听 `:8080`。数据持久化在 `./data` 目录。

### 反向代理 / HTTPS 部署

生产环境通常在前面加一层 Nginx（或 Caddy 等）做 TLS 终止，再把请求反代到容器的 `:8080`。此时 `PUBLIC_BASE_URL` 应填写对外的 HTTPS 地址，例如：

```bash
PUBLIC_BASE_URL=https://img.example.com
```

反代必须把原始协议和主机名透传给后端，否则网页里分片上传的 tus 地址会变成 `http://`，浏览器会按混合内容（mixed content）拦截，或被 Nginx 的 `http → https` 301 跳转打断 `PATCH`/`HEAD` 请求。服务端的 tus 处理器已开启 `RespectForwardedHeaders`，会读取下列由反向代理覆盖的头重建上传地址：

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host              $host;
    proxy_set_header X-Forwarded-Host  $host;
    proxy_set_header X-Forwarded-Proto $scheme;   # https
    proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;

    # tus 大文件分片上传需要
    proxy_http_version 1.1;
    proxy_request_buffering off;
    proxy_buffering off;
    proxy_read_timeout 24h;
    client_max_body_size 0;   # 不限，单文件上限由 MAX_UPLOAD_BYTES 控制
}
```

应用默认不使用 `X-Forwarded-*` 等代理请求头推导文件链接的主机名与协议。文件链接始终优先使用 `PUBLIC_BASE_URL`；未配置时，仅根据后端服务直接接收到的请求协议（是否开启 TLS）生成。生产环境应配置 `PUBLIC_BASE_URL=https://img.example.com`，并且只将应用的 `:8080` 端口暴露给反向代理，由反向代理覆盖上述 `X-Forwarded-*` 头。

## 功能特性

- **流式上传**：文件以流的方式写入磁盘，边写边计算 SHA-256 摘要，内存占用恒定。
- **内容去重**：上传完全相同的文件会返回已存在的 URL，不重复存储，也不重置创建时间与过期时间。
- **容量管控**：默认单文件上限 10 GiB、总存储上限 10 GiB（硬限制）。上传前会预留容量，确保并发临时上传不会突破总上限；超出总容量时返回 `507 Insufficient Storage`。
- **可配置类型校验**：通过 `ALLOWED_TYPES` 选择接受哪些内容类型。默认完全开放，可设为 `images` 仅接受 JPEG/PNG/GIF/WebP，也可自由组合预设别名（`images`/`videos`/`audio`/`docs`）、前缀通配（`image/*`、`video/*`）和精确 MIME。类型检测基于文件头 magic bytes（标准库 `http.DetectContentType`），不依赖文件名后缀。
- **自动过期**：默认保留 24 小时，服务启动时及每小时自动清理过期文件。
- **崩溃恢复**：启动时自动删除中断的临时上传，以及不在索引中的孤立存储对象，保证数据一致性。
- **可恢复大文件上传**：网页使用 Uppy Tus，服务端由 tusd 处理分片、进度、暂停、取消与刷新后的续传；上传元数据位于 `data/tus`。
- **双存储后端**：支持本地磁盘与阿里云 OSS；OSS 模式下文件的删除与去重仍由本服务管理。
- **元数据持久化**：文件索引保存在 `./data` 目录，重启后不丢失。

## 环境变量说明

所有配置通过环境变量或 `.env` 文件提供。复制 `.env.example` 为 `.env` 后按需修改：

程序启动时会自动读取当前工作目录的 `.env`；已有系统环境变量优先，便于 Docker Compose、CI 和生产部署覆盖本地文件配置。

| 变量名 | 默认值 | 说明 |
| --- | --- | --- |
| `LISTEN_ADDR` | `:8080` | 服务监听地址（容器内）。改宿主机对外端口请用 `HOST_PORT`。 |
| `DATA_DIR` | `/data` | 数据目录，存放文件与元数据索引，需放在持久化存储上。 |
| `PUBLIC_BASE_URL` | - | 客户端访问返回 URL 时使用的公网地址（局域网 IP 或 HTTPS 域名），末尾不带斜杠。本地存储模式下必填，否则 URL 不可达。 |
| `HOST_PORT` | `8080` | 宿主机对外暴露端口（docker compose 端口映射的宿主机侧）。容器内仍监听 `LISTEN_ADDR`（默认 `:8080`）。 |
| `AUTH_PASSWORD` | - | 必填。访问网页和外部上传 API 使用的短密码，长度为 6-16 个可打印 ASCII 字符。不要设置为常见密码。 |
| `MAX_UPLOAD_BYTES` | `10GiB` | 单文件上传上限，不得超过 10 GiB。支持原始字节数，以及 `MiB`、`MB`、`GiB`、`GB`、`m`、`g` 等单位。 |
| `MAX_STORAGE_BYTES` | `10GiB` | 所有未过期文件的总大小上限，不得超过 10 GiB。达到上限后新文件返回 `507`。 |
| `RETENTION` | `1d` | 文件保留时长。仅支持正整数加小写单位：`m`（分钟）、`h`（小时）、`d`（天）、`w`（周），如 `5h`、`1d`、`1w`。 |
| `ALLOWED_TYPES` | `all` | 接受的上传内容类型。见下方详细说明。 |
| `STORAGE_DRIVER` | `local` | 存储驱动，可选 `local`（本地磁盘）或 `oss`（阿里云 OSS）。选 `oss` 前须填齐下方所有 `OSS_*` 配置。 |
| `OSS_ENDPOINT` | - | 阿里云 OSS Endpoint，例如 `https://oss-cn-hangzhou.aliyuncs.com`。 |
| `OSS_BUCKET` | - | OSS Bucket 名称。 |
| `OSS_ACCESS_KEY_ID` | - | OSS AccessKey ID。 |
| `OSS_ACCESS_KEY_SECRET` | - | OSS AccessKey Secret。 |

服务每次启动都会生成新的内存签名密钥，因此浏览器登录状态和本地存储的文件分享链接都会失效；重启后重新登录或重新上传即可获得新链接。

> ⚠️ `.env` 包含敏感信息，切勿提交到版本库（已在 `.gitignore` 中忽略）。

### ALLOWED_TYPES 详细说明

`ALLOWED_TYPES` 控制上传时接受哪些内容类型。类型检测基于文件头 magic bytes（标准库 `http.DetectContentType`），不依赖文件名后缀。可用值：

| 取值 | 含义 |
| --- | --- |
| `all` | （默认）不做任何类型限制，接受所有内容。 |
| `images` | 仅图片：JPEG、PNG、GIF、WebP。 |
| `videos` | 视频：MP4、WebM、MOV、MKV、AVI、MPEG。 |
| `audio` | 音频：MP3、OGG、WAV、WebM audio、AAC、FLAC。 |
| `docs` | 文档：PDF、纯文本、Markdown、HTML。 |
| `image/*`、`video/*`、`application/*`… | 前缀通配，接受该大类下所有子类型。 |
| `image/png` | 精确匹配某个 MIME 类型。 |

以上可任意混用，逗号分隔。例如：

```bash
# 图片 + 视频 + PDF
ALLOWED_TYPES=images,videos,application/pdf
# 全部图片和音频
ALLOWED_TYPES=image/*,audio/*
# 完全不限制
ALLOWED_TYPES=all
```

不在白名单中的类型会返回 `400 Bad Request`，响应体包含实际检测到的 MIME 与当前允许的集合。

## 接口说明

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `POST` | `/api/upload?pwd={AUTH_PASSWORD}` | 上传文件，表单字段名为 `file`，返回 `{"url","created"}`。 |
| `POST/PATCH/HEAD/DELETE` | `/api/uploads/` | 标准 tus 可恢复上传端点，仅限已登录网页会话。 |
| `GET` | `/files/{sha256}?key={file-key}` | 读取本地存储的文件；上传响应会返回完整链接。 |
| `GET` | `/healthz` | 健康检查，返回 `204 No Content`，可用作容器探针。 |

`pwd` 是兼容命令行客户端的 URL 口令。它可能出现在代理日志、浏览器历史和命令历史中，因此仅应在可信网络中使用；网页上传使用登录会话，不会将密码放进 tus URL。

### 上传示例

```bash
curl -F "file=@picture.png" "http://localhost:8080/api/upload?pwd=eztCloud@"
```

成功响应：

```json
{
  "url": "http://localhost:8080/files/9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08?key=<file-specific-signature>",
  "created": "2026-07-16T00:00:00Z"
}
```

- 新文件返回 `201 Created`，重复文件返回 `200 OK`（内容与原有文件完全一致）。
- 文件过大返回 `413 Request Entity Too Large`。
- 容量已满返回 `507 Insufficient Storage`。
- 内容类型不在 `ALLOWED_TYPES` 白名单时返回 `400 Bad Request`。
- 未携带正确 `pwd` 时返回 `401 Unauthorized`；同一客户端连续错误尝试过多时返回 `429 Too Many Requests`。
- 返回的本地文件链接是仅对该文件有效的 bearer 链接；它不具备上传或访问其他文件的权限。请像密码一样妥善保存。
- 文件始终以附件形式下载，不会在本服务的同源页面中直接渲染。

## 过期与存储机制

- 服务在启动时及每小时执行清理：删除创建时间超过 `RETENTION` 的文件。
- 启动时还会移除上传中断遗留的临时文件，以及存在于存储中但不在索引里的孤立对象。
- 上传采用「先预留容量再写入」的策略，确保并发上传不会超出总容量上限。
- 未完成的 tus 上传在 2 小时无写入后自动清理并归还容量预留；服务重启后可继续未过期的上传。
- 文件内容与持久化元数据索引均保存在 `./data` 目录，请务必将其放在持久化卷上。

## 阿里云 OSS 模式

设置 `STORAGE_DRIVER=oss` 并填齐所有 `OSS_*` 配置即可启用：

- Bucket 应设为私有读。服务使用阿里云 OSS SDK 生成有效期等于文件保留时间的预签名下载 URL，无需公开读权限。
- OSS 下载 URL 是临时 bearer 链接；请像密码一样妥善保存，过期后重新上传即可获得新链接。
- 对象统一存放在 Bucket 内的 `image-host/` 前缀下。
- 文件的删除与去重仍由本服务管理，服务通过索引追踪每个对象，过期时自动删除。

## 其他部署方式

### 源码编译运行

需要本地已安装 Go 1.23+：

```bash
go build -o easy-temp-cloud .
./easy-temp-cloud
```

### 本地开发（自动重启）

开发时推荐使用 [Air](https://github.com/air-verse/air) 监听 Go 和网页资源文件。保存根目录或 `internal/` 下的 `.go` 文件，以及 `src/web/*.html`、`src/web/*.css`、`src/web/*.js` 或 `src/web/*.mjs` 后，Air 会重建并重启服务；因为网页资源通过 `go:embed` 编入二进制，Air 的代理会在新进程启动后自动刷新浏览器页面。

```powershell
go install github.com/air-verse/air@latest
air
```

Air 使用仓库根目录的 `.air.toml`，应用监听 `http://localhost:8080`，浏览器应通过支持自动刷新的代理入口 `http://localhost:8081` 访问。开发二进制写入并在退出时清理 `tmp/`。Docker Compose 仍用于生产部署，不受该配置影响。

### Docker 单独运行

不使用 Compose，直接用 `docker run`（需自行挂载数据卷并传入环境变量）：

```bash
docker build -t easy-temp-cloud .
docker run -d --name easy-temp-cloud \
  -p ${HOST_PORT:-8080}:8080 \
  -v "$PWD/data:/data" \
  -e PUBLIC_BASE_URL=http://你的服务器地址:${HOST_PORT:-8080} \
  -e AUTH_PASSWORD=short1 \
  easy-temp-cloud
```

## 许可

本项目按需自取。
