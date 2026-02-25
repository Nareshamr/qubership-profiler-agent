package actions

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Netcracker/qubership-profiler-agent/diagtools/constants"
	"github.com/Netcracker/qubership-profiler-agent/diagtools/log"
	"github.com/shirou/gopsutil/v4/process"
)

// GC log path patterns from JVM args (aligned with GCDumper.java).
// -Xloggc:<path> (Java 8 and earlier)
// -Xlog:...:file=<path> (Java 9+)
var (
	reXloggc   = regexp.MustCompile(`-Xloggc:(\S+)`)
	reXlogFile = regexp.MustCompile(`-Xlog[^:]*:file=(\S+)`)
)

type JavaGCLogsAction struct {
	Action
}

// CreateGCLogsAction creates an action to harvest GC logs from the Java process
// and upload them to the diagnostic center (when DCD is enabled).
func CreateGCLogsAction(ctx context.Context) (action JavaGCLogsAction, err error) {
	action = JavaGCLogsAction{
		Action: Action{
			DcdEnabled: constants.IsDcdEnabled(),
			DumpPath:   constants.DumpFolder(),
			PidName:    "java",
			CmdTimeout: 10 * time.Second,
		},
	}

	err = action.GetPodName(ctx)
	if err != nil {
		return
	}
	action.Pid, err = action.GetPid(ctx)
	if err != nil {
		return
	}

	return action, nil
}

// getJavaProcessCmdline returns the command line of the process with the given pid.
func getJavaProcessCmdline(pid int) (string, error) {
	proc, err := process.NewProcess(int32(pid))
	if err != nil {
		return "", err
	}
	s, err := proc.Cmdline()
	if err != nil {
		return "", err
	}
	// On Linux /proc/<pid>/cmdline uses null bytes between args; normalize so regex and path parsing work.
	return strings.ReplaceAll(s, "\x00", " "), nil
}

// trimPath removes null bytes and surrounding spaces from a path (e.g. from /proc cmdline).
func trimPath(s string) string {
	s = strings.TrimSpace(strings.TrimRight(s, "\x00"))
	return strings.TrimSpace(s)
}

// parseGCLogPathFromCmdline extracts the GC log file path from JVM arguments.
// It matches -Xloggc:<path> and -Xlog...:file=<path> (aligned with GCDumper.java).
func parseGCLogPathFromCmdline(cmdline string) (gcLogPath string, ok bool) {
	if m := reXloggc.FindStringSubmatch(cmdline); len(m) > 1 {
		return trimPath(m[1]), true
	}
	if m := reXlogFile.FindStringSubmatch(cmdline); len(m) > 1 {
		return trimPath(m[1]), true
	}
	return "", false
}

// findLatestGCLogFile returns the path to the current/latest GC log file in dir.
// Matches files whose name starts with baseName and ends with ".current" (Java 8)
// or equals baseName (Java 11+). Returns the file with the latest ModTime.
// baseName is normalized (no null bytes) so it matches actual files on disk.
func findLatestGCLogFile(dir, baseName string) (string, error) {
	baseName = trimPath(baseName)
	if baseName == "" {
		return "", nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	var candidates []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, baseName) {
			continue
		}
		// Java 8: <base>.0.current, <base>.1.current; Java 11+: <base> only or rotated <base>.0, <base>.1
		if name == baseName || strings.HasSuffix(name, ".current") {
			candidates = append(candidates, filepath.Join(dir, name))
		}
	}

	if len(candidates) == 0 {
		return "", nil
	}

	// Sort by ModTime descending (newest first)
	sort.Slice(candidates, func(i, j int) bool {
		infoI, _ := os.Stat(candidates[i])
		infoJ, _ := os.Stat(candidates[j])
		if infoI == nil || infoJ == nil {
			return false
		}
		return infoI.ModTime().After(infoJ.ModTime())
	})

	return candidates[0], nil
}

// GetGCLogs discovers the GC log folder from the Java process JVM arguments,
// reads the current GC log file from that folder, writes it to a timestamped file
// in the dump folder, and uploads it to the diagnostic center when enabled
// (similar to thread dump and heap dump).
func (action *JavaGCLogsAction) GetGCLogs(ctx context.Context) (err error) {
	pid, err := strconv.Atoi(action.Pid)
	if err != nil {
		log.Errorf(ctx, err, "failed to parse PID: %s", action.Pid)
		return err
	}

	cmdline, err := getJavaProcessCmdline(pid)
	if err != nil {
		log.Errorf(ctx, err, "failed to get command line for PID %s", action.Pid)
		return err
	}

	gcLogPath, ok := parseGCLogPathFromCmdline(cmdline)
	if !ok {
		log.Infof(ctx, "GetGCLogs: no GC log path found in JVM args for PID %s (Java must be started with -Xloggc:/path/to/gc.log or -Xlog:file=/path/to/gc.log)", action.Pid)
		return nil
	}
	gcLogPath = trimPath(gcLogPath)
	gcFolder := filepath.Clean(filepath.Dir(gcLogPath))
	gcBaseName := trimPath(filepath.Base(gcLogPath))

	latestPath, err := findLatestGCLogFile(gcFolder, gcBaseName)
	if err != nil {
		log.Errorf(ctx, err, "failed to list GC log folder %s", gcFolder)
		return err
	}
	if latestPath == "" {
		log.Infof(ctx, "GetGCLogs: no GC log file found in folder %s with baseName %s (expected a file named %s or %s.current)", gcFolder, gcBaseName, gcBaseName, gcBaseName)
		return nil
	}

	data, err := os.ReadFile(latestPath)
	if err != nil {
		log.Errorf(ctx, err, "failed to read GC log file %s", latestPath)
		return err
	}
	if len(data) == 0 {
		log.Infof(ctx, "GetGCLogs: GC log file is empty, skipping: %s", latestPath)
		return nil
	}

	err = action.GetDumpFile(constants.GCLogSuffix)
	if err != nil {
		log.Errorf(ctx, err, "GetGCLogs: failed to get dump file path")
		return err
	}

	err = os.WriteFile(action.DumpPath, data, 0644)
	if err != nil {
		log.Errorf(ctx, err, "failed to write GC log to %s", action.DumpPath)
		return err
	}

	if action.DcdEnabled && len(data) > 0 {
		log.Info(ctx, "GetGCLogs: DCD enabled, uploading to diagnostic center")
		err = action.GetTargetUrl(ctx)
		if err == nil {
			err = action.UploadOutputToDiagnosticCenter(ctx, data)
		}
	} else if len(data) > 0 {
		log.Infof(ctx, "GetGCLogs: GC log written to %s (upload skipped: DIAGNOSTIC_CENTER_DUMPS_ENABLED=false or unset)", action.DumpPath)
	}

	return err
}
