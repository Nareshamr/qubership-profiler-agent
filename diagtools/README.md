# Diagtools

The `diagtools` is a CLI tool to work with java heap and thread dumps, CPU usage, schedule some tasks or
export ZooKeeper config.

Supported features:

- `diagtools heap zip upload`
  - heap subcommand. Runs `jmap -dump:format=b,file={{.DumpPath}} {{.Pid}}` to collect heap dump for java pid and
    put it in the file specified.
  - zip parameter is optional. Zips heap file with default compression level. Compression level can be configured with
    NC_HEAP_DUMP_COMPRESSION_LEVEL environment variable:
    - NoCompression      = 0
    - BestSpeed          = 1
    - BestCompression    = 9
    - DefaultCompression = -1
      see [golang:deflate.go](https://github.com/golang/go/blob/master/src/compress/flate/deflate.go#L15-L18)
  - upload parameter is optional. Uploads zipped heap dump to collector service diagnostic controller
    via REST `http://localhost:8081/diagnostic/{namespace}/*/*/*/*/*/*/{podName}/{dumpName}`.
    Works together with zip flag only.

- `diagtools dump`
  - "dump" subcommand is responsible for collecting java thread dump (`jstack -l "{{.Pid}}"`),
    GC logs (when enabled), and CPU usage for java pid (`top -Hb -p{{.Pid}} -oTIME+ -d60 -n1`).
    NC_DIAGNOSTIC_THREADDUMP_ENABLED, ESC_HARVEST_GCLOG and NC_DIAGNOSTIC_TOP_ENABLED control what is collected.
    If environment variable is absent, thread/top are treated as on; GC log harvest is off unless ESC_HARVEST_GCLOG=true.

- `diagtools scan /tmp/diagnostic/*.hprof* ./core* ./hs_err*`
  - "scan" subcommand is responsible for finding files matching patterns, zipping (if necessary) ".hprof" and
    uploading ".hprof.zip" and other found files to collector service diagnostic controller via
    REST `http://localhost:8081/diagnostic/{namespace}/*/*/*/*/*/*/{podName}/{dumpName}`.

- `diagtools schedule`
  - "schedule" subcommand is responsible for collecting dumps(like dump subcommand),
    scanning(like scan subcommand) and cleaning logs located in NC_DIAGNOSTIC_LOGS_FOLDER by schedule.
    Interval can be changed via DIAGNOSTIC_DUMP_INTERVAL(default 1m), DIAGNOSTIC_SCAN_INTERVAL(default 3m) and
    KEEP_LOGS_INTERVAL(default 2 days) environment variables.

- `diagtools zkConfig "${NC_DIAGNOSTIC_FOLDER}/zkproperties" esc.config NC_DIAGNOSTIC_ESC_ENABLED ...`
  - "zkConfig" subcommand is responsible for changing nc-diagnostic-agent settings in case when ZOOKEEPER_ENABLED=true
    The first parameter is a path to zookeeper property file.
    The second and further ones are the zookeeper properties which are to be changed.

Environment variables used by tool:

- NC_DIAGNOSTIC_FOLDER path - to diagnostic folder. Default is `/tmp/diagnostic`.
- NC_DIAGNOSTIC_LOGS_FOLDER - path to logs. Default is `/tmp/diagnostic/log`.
- LOG_FILE_SIZE - size of log file in MB. Default is 1.
- LOG_FILE_BACKUPS - number of log file backups. Default is 5.
- KEEP_LOGS_INTERVAL - logs located in NC_DIAGNOSTIC_LOGS_FOLDER rotation interval in days. Default is 2.
- LOG_TO_CONSOLE - indicates if send logs to the console. Default is false.
- DIAGNOSTIC_CENTER_DUMPS_ENABLED - used to check if upload dumps to diagnostic center.
- NC_DIAGNOSTIC_THREADDUMP_ENABLED - used to check if thead dumps enabled.
- ESC_HARVEST_GCLOG - set to `true` to harvest GC logs from the Java process (from JVM -Xloggc / -Xlog:file= path) and upload to diagnostic center. Disabled by default.
- NC_DIAGNOSTIC_TOP_ENABLED - used to check if top dumps enabled.
- DIAGNOSTIC_DUMP_INTERVAL - dump interval used in case of schedule. Default is 1 minute. Support go Duration format.
- DIAGNOSTIC_SCAN_INTERVAL - scan interval used in case of schedule. Default is 1 minutes. Support go Duration format.
- NC_DIAGNOSTIC_AGENT_SERVICE - diagnostic agent service name. Default is `nc-diagnostic-agent`.
- PROFILER_FOLDER -path to profiler folder. Default is `/app/diag`
- ZOOKEEPER_ENABLED - used to check if zookeeper enabled. Default is false.
- CLOUD_NAMESPACE - contains actual microservice namespace. Can't be empty.
- MICROSERVICE_NAME - contains actual microservice name. Can't be empty.
- ZOOKEEPER_ADDRESS - zookeeper address for fetch settings from ZooKeeper.
- NC_HEAP_DUMP_COMPRESSION_LEVEL - defines heap dump compression level. Default is `-1`.
- NC_DIAG_FILE_UPLOAD_TIMEOUT_MINUTES - HTTP client timeout for file uploads (heap dumps, etc.) in minutes. Used when uploading `.hprof.zip` and other dumps to the collector; large multi-GB uploads can exceed a short timeout. Default is `120` (2 hours). Set to a positive integer to override (e.g. `60` for 1 hour, `360` for 6 hours).
- ESC_LOG_FORMAT - used to set custom log format for agent loggers using java logging service
- LOGBACK_CLOUD_AGENT_LOG_FORMAT - Used to set custom log format for agent loggers using logback service.

## Testing GC logs

To run the project and verify that GC logs are created and sent to the diagnostics center:

### 1. Build

From the `diagtools` directory:

```bash
cd diagtools
go build -o diagtools .
```

Note: On Windows the full build may fail due to the `jattach` dependency; build on Linux or in a container (e.g. Docker) if needed.

### 2. Start a Java process with GC logging

The agent discovers the GC log path from JVM arguments. Start a Java app with one of:

- **Java 8:** `-Xloggc:/tmp/myapp/gc.log` (creates e.g. `gc.log.0.current` in that directory)
- **Java 9+:** `-Xlog:gc*:file=/tmp/myapp/gc.log` or `-Xlog:gc:file=/tmp/myapp/gc.log`

Example:

```bash
mkdir -p /tmp/myapp
java -Xloggc:/tmp/myapp/gc.log -cp your-app.jar com.example.Main
# or Java 11+: java -Xlog:gc*:file=/tmp/myapp/gc.log -cp your-app.jar com.example.Main
```

Leave this process running so the agent can find its PID and command line.

### 3. Set environment variables

- **Enable GC log harvest:** `ESC_HARVEST_GCLOG=true`
- **Enable upload to diagnostic center:** `DIAGNOSTIC_CENTER_DUMPS_ENABLED=true`
- **Dump folder** (where the timestamped `.gclog` file is written): e.g. `NC_DIAGNOSTIC_LOGS_FOLDER=/tmp/diagnostic` (default `/tmp/diagnostic` if unset)
- **Pod name** (for upload path): set via `NC_DIAGNOSTIC_FOLDER` or `PROFILER_FOLDER` with a `pod.name` file, or the tool falls back to hostname. For a quick test you can create:
  - `mkdir -p /app/ncdiag && echo -n my-pod-1 > /app/ncdiag/pod.name`
- **Diagnostic service** (upload URL): `NC_DIAGNOSTIC_AGENT_SERVICE=http://nc-diagnostic-agent:8080` (or your collector URL)
- **Namespace:** `CLOUD_NAMESPACE=my-namespace` (required for upload URL)

Example for a local test (upload only if your diagnostic center is reachable):

```bash
export ESC_HARVEST_GCLOG=true
export DIAGNOSTIC_CENTER_DUMPS_ENABLED=true
export NC_DIAGNOSTIC_LOGS_FOLDER=/tmp/diagnostic
export CLOUD_NAMESPACE=default
# Optional: log to console to see messages
export LOG_TO_CONSOLE=true
```

### 4. Run the dump command

```bash
./diagtools dump
```

Or run on a schedule (dumps every `DIAGNOSTIC_DUMP_INTERVAL`, default 1m):

```bash
./diagtools schedule
```

### 5. Verify GC logs are created and sent

- **Local file:** A timestamped file should appear under `NC_DIAGNOSTIC_LOGS_FOLDER`, e.g. `/tmp/diagnostic/20260102T120000.gclog`. Check that it contains GC log lines.
- **Logs:** With `LOG_TO_CONSOLE=true` you should see messages such as:
  - `start collecting GC logs for PID #<pid>`
  - `collecting GC log from <path> to <dumpPath> (size N bytes)`
  - `uploaded <dumpPath>` when the file is sent to the diagnostic center.
- **Diagnostic center:** If upload is enabled and the service is reachable, the same file should appear under the diagnostic API path: `.../diagnostic/{namespace}/{date}/{podName}/{timestamp}.gclog`.
