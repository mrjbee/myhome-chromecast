# myhome-chromecast

A small standalone HTTP service that continuously discovers Google Cast devices and controls one active connection at a time. Its primary use case is starting YouTube videos from Node-RED.

## Requirements

- Go 1.25 or newer
- Linux with access to the local network and multicast DNS

## Build and run

Build a static binary for the local Linux AMD64 development machine:

```sh
make build
```

Run the service locally:

```sh
make run
```

The service listens on `:8080` by default.

Build a static Linux ARM64 binary for Raspberry Pi 5:

```sh
make build-arm64
```

The resulting binaries are written to `bin/`.

## Docker deployment on Raspberry Pi 5

The Docker image is built for `linux/arm64` by default. Build it locally without pushing:

```sh
make docker-build
```

Build, load, and push it to the Raspberry Pi registry through the local Docker daemon:

```sh
make docker-push
```

The default image reference is:

```text
raspberrypi.lan:5000/org.monroe.team/myhome-chromecast:latest
```

The registry, image name, tag, and platform can be overridden when needed:

```sh
make docker-push \
  REGISTRY=raspberrypi.lan:5000 \
  IMAGE_NAME=org.monroe.team/myhome-chromecast \
  TAG=latest \
  PLATFORM=linux/arm64
```

Pull and run the service on Raspberry Pi OS:

```sh
docker pull raspberrypi.lan:5000/org.monroe.team/myhome-chromecast:latest

docker run -d \
  --name myhome-chromecast \
  --restart unless-stopped \
  --network host \
  -e HTTP_ADDR=:8080 \
  -e DEVICE_TTL=3h \
  raspberrypi.lan:5000/org.monroe.team/myhome-chromecast:latest
```

The equivalent minimal Docker Compose service is:

```yaml
services:
  myhome-chromecast:
    image: raspberrypi.lan:5000/org.monroe.team/myhome-chromecast:latest
    container_name: myhome-chromecast
    restart: unless-stopped
    network_mode: host
    environment:
      HTTP_ADDR: ":8080"
```

Save it as `compose.yaml` on the Raspberry Pi and start the service with:

```sh
docker compose up -d
```

`HTTP_ADDR` is the only environment variable set in this minimal example. It can be omitted as well because `:8080` is the application default. No `ports` section is needed when host networking is enabled.

Host networking is required for reliable multicast DNS discovery on Linux. If the registry uses plain HTTP, configure `raspberrypi.lan:5000` as an insecure registry in the Docker daemon on both the build machine and the Raspberry Pi.

## Configuration

Configuration is provided through environment variables:

| Variable | Default | Description |
|---|---:|---|
| `HTTP_ADDR` | `:8080` | HTTP server listen address |
| `DISCOVERY_INTERFACE` | empty | Network interface used for mDNS; empty enables automatic selection |
| `DEVICE_TTL` | `3h` | Time a device remains available after its last mDNS announcement |
| `CAST_TIMEOUT` | `5s` | Timeout for a request to a Cast device |
| `YOUTUBE_TIMEOUT` | `15s` | Timeout for a YouTube Lounge HTTP request |

Device discovery runs continuously for the lifetime of the service. Expired registry entries are removed lazily when the registry is queried; there is no background cleanup worker.

When running in a container, the service needs access to the local network and multicast DNS traffic on `224.0.0.251:5353`. The service does not require persistent storage.

## HTTP API

| Method | Path | Description |
|---|---|---|
| `GET` | `/devices` | Return the current discovery registry snapshot |
| `POST` | `/devices/{uuid}/connect` | Connect to a discovered device |
| `GET` | `/devices/connected` | Return the connected device and its current registry information |
| `DELETE` | `/devices/connect` | Close the active connection |
| `GET` | `/devices/connected/status` | Fetch the current Cast receiver status |
| `GET` | `/devices/connected/volume` | Fetch the current receiver volume |
| `PUT` | `/devices/connected/volume` | Set the receiver volume |
| `POST` | `/devices/connected/media` | Run a media command |
| `POST` | `/devices/connected/youtube` | Start a YouTube video |

All connected-device operations verify that the device is still present in the discovery registry. If its registry entry has expired, the service returns `404 Not Found`. `DELETE /devices/connect` remains available so that an active connection can always be closed.

### Discover and connect

```sh
curl http://localhost:8080/devices

curl -X POST \
  http://localhost:8080/devices/CHROMECAST_UUID/connect
```

Only one Cast connection can be active. Connecting again closes the existing connection before opening the new one.

### Receiver status

```sh
curl http://localhost:8080/devices/connected/status
```

### Volume

Google Cast uses a native volume range of `0.0` through `1.0`.

```sh
curl http://localhost:8080/devices/connected/volume

curl -X PUT \
  http://localhost:8080/devices/connected/volume \
  -H 'Content-Type: application/json' \
  -d '{"level":0.5}'
```

### Media control

Supported commands are `play`, `pause`, and `stop`.

```sh
curl -X POST \
  http://localhost:8080/devices/connected/media \
  -H 'Content-Type: application/json' \
  -d '{"command":"pause"}'
```

### YouTube

```sh
curl -X POST \
  http://localhost:8080/devices/connected/youtube \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ"}'
```

The YouTube integration reuses an existing YouTube Cast session when possible. Otherwise it launches receiver `233637DE`, obtains a `screenId` through `urn:x-cast:com.google.youtube.mdx`, binds a YouTube Lounge session, and sends `setPlaylist(videoId)`.

If an existing YouTube receiver session does not return a `screenId`, the service treats it as unresponsive, stops that receiver session, launches a fresh one, and retries the MDX handshake automatically.

YouTube Lounge is an unofficial protocol and may change without notice.

## Logging

The service writes structured JSON logs to standard output. It logs:

- HTTP server startup and shutdown;
- initial and repeated mDNS announcements for every Cast device at `INFO` level;
- Cast connection attempts and successful connections;
- disconnections, including reconnects, device switches, and service shutdown;
- request and connection failures.
