package main

import (
    "context"
    "encoding/json"
    "flag"
    "fmt"
    "log"
    "net/http"
    "strings"
    "sync"
    "time"

    "github.com/docker/docker/api/types"
    "github.com/docker/docker/client"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
    // These can be overridden during build with -ldflags
    appName      = "dockerstats"
    fullProgName = "Simple Docker Stats Prometheus Exporter"
    version      = "0.1.0"
)

var (
    port       = flag.Int("port", 9487, "Port to expose metrics")
    interval   = flag.Int("interval", 15, "Interval in seconds (min: 3)")
    hostIP     = flag.String("hostip", "", "Docker host IP (for TCP connection)")
    hostPort   = flag.Int("hostport", 0, "Docker host port (for TCP connection)")
    maxWorkers = flag.Int("workers", 10, "Max concurrent API calls")
    // Both -v and --version / -version will work
    showVer      = flag.Bool("version", false, "Show version and exit")
    showVerShort = flag.Bool("v", false, "Show version and exit (short)")
)

// Internal storage for CPU deltas (since Docker OneShot stats often have PreCPU=0)
type cpuSnapshot struct {
    totalUsage  uint64
    systemUsage uint64
    lastSeen    time.Time
    name        string
}

var (
    cpuHistory   = make(map[string]cpuSnapshot)
    historyMutex sync.RWMutex
    registry     = prometheus.NewRegistry()

    // Metrics Gauges
    gaugeCpu        = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: appName + "_cpu_usage_ratio"}, []string{"name", "id"})
    gaugeMemBytes   = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: appName + "_memory_usage_bytes"}, []string{"name", "id"})
    gaugeMemRss     = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: appName + "_memory_usage_rss_bytes"}, []string{"name", "id"})
    gaugeMemLimit   = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: appName + "_memory_limit_bytes"}, []string{"name", "id"})
    gaugeMemRatio   = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: appName + "_memory_usage_ratio"}, []string{"name", "id"})
    gaugeNetRx      = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: appName + "_network_received_bytes"}, []string{"name", "id"})
    gaugeNetTx      = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: appName + "_network_transmitted_bytes"}, []string{"name", "id"})
    gaugeBlockRead  = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: appName + "_blockio_read_bytes"}, []string{"name", "id"})
    gaugeBlockWrite = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: appName + "_blockio_written_bytes"}, []string{"name", "id"})
)

func init() {
    registry.MustRegister(gaugeCpu, gaugeMemBytes, gaugeMemRss, gaugeMemLimit, gaugeMemRatio, gaugeNetRx, gaugeNetTx, gaugeBlockRead, gaugeBlockWrite)
}

func main() {
    flag.Usage = func() {
        fmt.Fprintf(flag.CommandLine.Output(), "%s (Version: %s)\n\nUsage:\n", fullProgName, version)
        flag.PrintDefaults()
    }
    flag.Parse()

    if *showVer || *showVerShort {
        fmt.Printf("%s (Version: %s)\n", fullProgName, version)
        return
    }
    if *interval < 3 { *interval = 3 }

    var cli *client.Client
    var err error

    // Connection Logic
    if *hostIP != "" && *hostPort != 0 {
        hostAddr := fmt.Sprintf("tcp://%s:%d", *hostIP, *hostPort)
        log.Printf("INFO: Connecting to Docker on %s...", hostAddr)
        cli, err = client.NewClientWithOpts(
            client.WithHost(hostAddr),
            client.WithAPIVersionNegotiation(),
        )
    } else {
        log.Printf("INFO: Connecting to Docker on default socket (/var/run/docker.sock)...")
        cli, err = client.NewClientWithOpts(
            client.FromEnv,
            client.WithAPIVersionNegotiation(),
        )
    }

    if err != nil {
        log.Fatalf("FATAL: Unable to create Docker client: %v", err)
    }

    // Initial Ping check (Fail Fast)
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    if _, err := cli.Ping(ctx); err != nil {
        cancel()
        log.Fatalf("FATAL: Could not connect to Docker: %v", err)
    }
    cancel()
    log.Printf("INFO: Connection established")

    // Background polling
    go func() {
        for {
            gatherMetrics(cli)
            cleanupHistory()
            time.Sleep(time.Duration(*interval) * time.Second)
        }
    }()

    // Server setup
    http.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
    http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("OK")) })

    log.Printf("INFO: %s listening on :%d", fullProgName, *port)
    if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), nil); err != nil {
        log.Fatalf("ERROR: Server failed: %v", err)
    }
}

func gatherMetrics(cli *client.Client) {
    ctx := context.Background()
    containers, err := cli.ContainerList(ctx, types.ContainerListOptions{})
    if err != nil {
        log.Printf("ERROR: ContainerList: %v", err)
        return
    }

    var wg sync.WaitGroup
    semaphore := make(chan struct{}, *maxWorkers)

    for _, c := range containers {
        wg.Add(1)
        go func(cid string, cnames []string) {
            defer wg.Done()
            semaphore <- struct{}{}
            defer func() { <-semaphore }()

            stats, err := cli.ContainerStatsOneShot(ctx, cid)
            if err != nil { return }
            defer stats.Body.Close()

            var v types.StatsJSON
            if err := json.NewDecoder(stats.Body).Decode(&v); err != nil { return }

            name := "unknown"
            if len(cnames) > 0 { name = strings.TrimPrefix(cnames[0], "/") }
            labels := prometheus.Labels{"name": name, "id": cid[:12]}

            // --- CPU Calculation (Self-managed Delta) ---
            currentTotal := v.CPUStats.CPUUsage.TotalUsage
            currentSystem := v.CPUStats.SystemUsage

            historyMutex.RLock()
            prev, found := cpuHistory[cid]
            historyMutex.RUnlock()

            if found {
                cpuDelta := float64(currentTotal) - float64(prev.totalUsage)
                systemDelta := float64(currentSystem) - float64(prev.systemUsage)
                onlineCPUs := float64(v.CPUStats.OnlineCPUs)
                if onlineCPUs == 0 { onlineCPUs = float64(len(v.CPUStats.CPUUsage.PercpuUsage)) }

                if systemDelta > 0 && cpuDelta > 0 {
                    cpuPercent := (cpuDelta / systemDelta) * onlineCPUs * 100.0
                    gaugeCpu.With(labels).Set(cpuPercent)
                }
            } else {
                log.Printf("INFO: New container detected: %s (id: %s)", name, cid[:12])
            }

            // Save state for next tick
            historyMutex.Lock()
            cpuHistory[cid] = cpuSnapshot{
                totalUsage:  currentTotal,
                systemUsage: currentSystem,
                lastSeen:    time.Now(),
                name:        name,
            }
            historyMutex.Unlock()

            // --- Memory ---
            memUsage := float64(v.MemoryStats.Usage)
            memLimit := float64(v.MemoryStats.Limit)
            gaugeMemBytes.With(labels).Set(memUsage)
            gaugeMemLimit.With(labels).Set(memLimit)
            if memLimit > 0 { gaugeMemRatio.With(labels).Set((memUsage / memLimit) * 100.0) }
            if rss, ok := v.MemoryStats.Stats["rss"]; ok { gaugeMemRss.With(labels).Set(float64(rss)) }

            // --- Network ---
            for _, net := range v.Networks {
                gaugeNetRx.With(labels).Set(float64(net.RxBytes))
                gaugeNetTx.With(labels).Set(float64(net.TxBytes))
                break
            }

            // --- Block IO ---
            var r, w uint64
            for _, bio := range v.BlkioStats.IoServiceBytesRecursive {
                switch strings.ToLower(bio.Op) {
                case "read": r += bio.Value
                case "write": w += bio.Value
                }
            }
            gaugeBlockRead.With(labels).Set(float64(r))
            gaugeBlockWrite.With(labels).Set(float64(w))

        }(c.ID, c.Names)
    }
    wg.Wait()
}

func cleanupHistory() {
    historyMutex.Lock()
    defer historyMutex.Unlock()
    for id, snap := range cpuHistory {
        if time.Since(snap.lastSeen) > time.Duration(*interval)*2*time.Second {
            log.Printf("INFO: Container gone: %s (id: %s). Removing from tracking.", snap.name, id[:12])

            // Clean up Prometheus metrics so they don't stay in /metrics forever
            l := prometheus.Labels{"name": snap.name, "id": id[:12]}
            gaugeCpu.Delete(l)
            gaugeMemBytes.Delete(l)
            gaugeMemRss.Delete(l)
            gaugeMemLimit.Delete(l)
            gaugeMemRatio.Delete(l)
            gaugeNetRx.Delete(l)
            gaugeNetTx.Delete(l)
            gaugeBlockRead.Delete(l)
            gaugeBlockWrite.Delete(l)

            delete(cpuHistory, id)
        }
    }
}
