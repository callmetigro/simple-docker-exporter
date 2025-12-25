# Simple docker exporter

A lightweight Prometheus exporter for Docker container statistics, ported from JavaScript to Go for maximum efficiency and minimal resource footprint.

## Motivation

In modern containerized environments, monitoring is crucial. However, popular solutions like **cAdvisor** can sometimes consume significant CPU resources. 

I was looking for a lighter alternative and found the excellent [docker_stats_exporter](https://github.com/wywywywy/docker_stats_exporter) by **wywywywy**. While the original Node.js implementation worked perfectly, it inspired me to create this **Go port**. By moving to Go, the exporter becomes a single static binary with a tiny memory footprint (~10MB RAM) and significantly lower CPU overhead.

## Features

- **Resource Efficient:** Written in Go, minimal overhead compared to Node.js or cAdvisor.
- **Accurate CPU Metrics:** Manages internal state to calculate precise CPU usage deltas.
- **Fail-Fast:** Validates Docker connection on startup and exits if the socket is missing.
- **Flexible:** Supports both Unix Socket and TCP connections to Docker.
- **Clean Metrics:** Automatically cleans up data for removed containers.

## Metrics

The exporter provides the following metrics (prefixed with `dockerstats_`):

| Metric | Description |
| :--- | :--- |
| `cpu_usage_ratio` | CPU usage percentage (0-100%) |
| `memory_usage_bytes` | Current memory usage in bytes |
| `memory_usage_rss_bytes` | Memory RSS usage in bytes |
| `memory_limit_bytes` | Container memory limit |
| `memory_usage_ratio` | Memory usage percentage (0-100%) |
| `network_received_bytes` | Network bytes received |
| `network_transmitted_bytes` | Network bytes transmitted |
| `blockio_read_bytes` | Block IO read bytes |
| `blockio_written_bytes` | Block IO written bytes |

## Usage

### Using Docker

```bash
docker run -d \
  --name simple-docker-exporter \
  --restart always \
  -p 9487:9487 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  callmetigro/simple-docker-exporter:latest
```

### Using Docker Compose

```yaml
services:
  exporter:
    image: callmetigro/simple-docker-exporter:latest
    container_name: docker-stats-exporter
    restart: always
    ports:
      - "9487:9487"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    # command: ["-interval", "10", "-workers", "15"] # Optional flags
```

### Configuration Flags

| Flag | Default | Description |
| :--- | :--- | :--- |
| `-port` | 9487 | Port to expose Prometheus metrics |
| `-interval` | 15 | Polling interval in seconds (min: 3) |
| `-workers` | 10 | Max concurrent calls to Docker API |
| `-hostip` | "" | Docker host IP (for TCP) |
| `-hostport` | 0 | Docker host port (for TCP) |
| `-v`, `--version` | | Show version and exit |

## Credits

- Original Node.js project: [wywywywy/docker_stats_exporter](https://github.com/wywywywy/docker_stats_exporter)
- Inspiration: Seeking a lightweight alternative to cAdvisor.

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

This work is a Go port of the original [docker_stats_exporter](https://github.com/wywywywy/docker_stats_exporter) by @wywywywy, which is also licensed under
