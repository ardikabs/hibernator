/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package cli

import (
	"bufio"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

type logsOptions struct {
	root   *rootOptions
	target string
	tail   int64
	follow bool
	level  string
}

// newLogsCommand creates the "logs" command.
func newLogsCommand(opts *rootOptions) *cobra.Command {
	logsOpts := &logsOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "logs <plan-name>",
		Short: "View controller logs filtered by plan context",
		Long: `Fetch logs from the Hibernator controller pod and filter them
by plan name, target, or execution ID.

The command discovers the controller pod automatically by label selector
(app.kubernetes.io/name=hibernator), then streams or tails its logs.

Examples:
  kubectl hibernator logs my-plan
  kubectl hibernator logs my-plan --tail 100
  kubectl hibernator logs my-plan --target my-cluster
  kubectl hibernator logs my-plan --follow
  kubectl hibernator logs my-plan --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(cmd.Context(), logsOpts, args[0])
		},
	}

	cmd.Flags().StringVar(&logsOpts.target, "target", "", "Filter logs by target name")
	cmd.Flags().StringVar(&logsOpts.level, "level", "", "Filter logs by level: 'error' (logs with error field) or 'info' (logs without errors)")
	cmd.Flags().Int64Var(&logsOpts.tail, "tail", 500, "Number of recent log lines to fetch")
	cmd.Flags().BoolVarP(&logsOpts.follow, "follow", "f", false, "Follow log output (stream)")

	return cmd
}

func runLogs(ctx context.Context, opts *logsOptions, planName string) error {
	// Build kubeconfig
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if opts.root.kubeconfig != "" {
		loadingRules.ExplicitPath = opts.root.kubeconfig
	}

	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	restConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		return fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	// Build controller-runtime client for fetching plan info
	k8sClient, err := newK8sClient(opts.root)
	if err != nil {
		return err
	}

	// Fetch the plan to get execution IDs for filtering
	ns := resolveNamespace(opts.root)
	var plan hibernatorv1alpha1.HibernatePlan
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: planName, Namespace: ns}, &plan); err != nil {
		return fmt.Errorf("failed to get HibernatePlan %q in namespace %q: %w", planName, ns, err)
	}

	// Discover controller namespace and fetch all running controller pods
	controllerNS := discoverControllerNamespace()

	var podList corev1.PodList
	if err := k8sClient.List(ctx, &podList,
		client.InNamespace(controllerNS),
		client.MatchingLabels{"app.kubernetes.io/name": "hibernator"},
	); err != nil {
		return fmt.Errorf("failed to list controller pods in namespace %q: %w", controllerNS, err)
	}

	if len(podList.Items) == 0 {
		return fmt.Errorf("no controller pod found with label app.kubernetes.io/name=hibernator in namespace %q", controllerNS)
	}

	// Filter running pods
	var runningPods []*corev1.Pod
	for i := range podList.Items {
		if podList.Items[i].Status.Phase == corev1.PodRunning {
			runningPods = append(runningPods, &podList.Items[i])
		}
	}
	if len(runningPods) == 0 {
		runningPods = append(runningPods, &podList.Items[0])
	}

	// Build clientset for pod logs API
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	// Build filter context from plan
	filter := buildLogFilter(planName, opts, &plan)

	// Fetch logs from all controller pods
	if opts.follow {
		return followLogsFromPods(ctx, clientset, runningPods, opts, filter)
	}

	return tailLogsFromPods(ctx, clientset, runningPods, opts, filter)
}

// logLine represents a parsed log entry with timestamp for sorting.
type logLine struct {
	raw       string    // Original log raw line
	timestamp time.Time // Parsed timestamp
	hash      string    // Content hash for deduplication
	line      string    // Formatted line
}

// tailLogsFromPods fetches and aggregates logs from all controller pods, sorts by timestamp, and displays.
func tailLogsFromPods(ctx context.Context, clientset *kubernetes.Clientset, pods []*corev1.Pod, opts *logsOptions, filter *logFilter) error {
	logChan := make(chan *logLine, 1000)
	errChan := make(chan error, len(pods))
	var wg sync.WaitGroup

	// Fetch logs from each pod concurrently
	for _, pod := range pods {
		wg.Add(1)
		go func(p *corev1.Pod) {
			defer wg.Done()
			if err := fetchAndFilterPodLogs(ctx, clientset, p, opts, filter, logChan); err != nil {
				errChan <- fmt.Errorf("pod %s/%s: %w", p.Namespace, p.Name, err)
			}
		}(pod)
	}

	// Close channel when all goroutines finish
	go func() {
		wg.Wait()
		close(logChan)
	}()

	// Collect all logs
	var allLogs []*logLine
	for log := range logChan {
		allLogs = append(allLogs, log)
	}

	// Check for errors
	close(errChan)
	for err := range errChan {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	// Sort by timestamp
	sort.Slice(allLogs, func(i, j int) bool {
		if allLogs[i].timestamp.Equal(allLogs[j].timestamp) {
			// Break ties by hash for consistency
			return allLogs[i].hash < allLogs[j].hash
		}
		return allLogs[i].timestamp.Before(allLogs[j].timestamp)
	})

	// Deduplicate and display
	seen := make(map[string]bool)
	for _, log := range allLogs {
		if !seen[log.hash] {
			seen[log.hash] = true
			if opts.root.jsonOutput {
				fmt.Println(log.raw)
			} else {
				fmt.Println(log.line)
			}
		}
	}

	return nil
}

// followLogsFromPods streams logs from all controller pods concurrently, merging by timestamp.
func followLogsFromPods(ctx context.Context, clientset *kubernetes.Clientset, pods []*corev1.Pod, opts *logsOptions, filter *logFilter) error {
	logChan := make(chan *logLine, 1000)
	errChan := make(chan error, len(pods))
	var wg sync.WaitGroup

	// Show which pods we're querying
	fmt.Fprintf(os.Stderr, "Following logs from %d controller pod(s):\n", len(pods))
	for _, pod := range pods {
		fmt.Fprintf(os.Stderr, "  - %s/%s\n", pod.Namespace, pod.Name)
	}
	fmt.Fprintf(os.Stderr, "\n")

	// Stream logs from each pod concurrently
	for _, pod := range pods {
		wg.Add(1)
		go func(p *corev1.Pod) {
			defer wg.Done()
			logOpts := &corev1.PodLogOptions{
				Follow: true,
			}
			if err := streamPodLogs(ctx, clientset, p, opts, filter, logOpts, logChan); err != nil {
				errChan <- fmt.Errorf("pod %s/%s: %w", p.Namespace, p.Name, err)
			}
		}(pod)
	}

	// Close channel when all goroutines finish
	go func() {
		wg.Wait()
		close(logChan)
	}()

	// Collect and deduplicate logs as they arrive
	seen := make(map[string]bool)
	seenMutex := &sync.Mutex{}

	for log := range logChan {
		seenMutex.Lock()
		if !seen[log.hash] {
			seen[log.hash] = true
			if opts.root.jsonOutput {
				fmt.Println(log.raw)
			} else {
				fmt.Println(log.line)
			}
		}
		seenMutex.Unlock()
	}

	// Report any errors
	close(errChan)
	for err := range errChan {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}

	return nil
}

// fetchAndFilterPodLogs reads logs from a single pod, filters them, and sends to channel.
func fetchAndFilterPodLogs(ctx context.Context, clientset *kubernetes.Clientset, pod *corev1.Pod, opts *logsOptions, filter *logFilter, logChan chan<- *logLine) error {
	logOpts := &corev1.PodLogOptions{}
	if opts.tail > 0 {
		logOpts.TailLines = &opts.tail
	}

	req := clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, logOpts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()

	return parseLogs(stream, filter, logChan)
}

// streamPodLogs streams logs from a single pod in follow mode.
func streamPodLogs(ctx context.Context, clientset *kubernetes.Clientset, pod *corev1.Pod, opts *logsOptions, filter *logFilter, logOpts *corev1.PodLogOptions, logChan chan<- *logLine) error {
	req := clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, logOpts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()

	return parseLogs(stream, filter, logChan)
}

// parseLogs reads a log stream, filters lines, and sends to channel.
func parseLogs(stream io.ReadCloser, filter *logFilter, logChan chan<- *logLine) error {
	scanner := bufio.NewScanner(stream)
	// Increase buffer for long log lines
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if filter.matches(line) {
			// Parse timestamp from JSON
			var logEntry map[string]interface{}
			ts := time.Now()
			if err := json.Unmarshal([]byte(line), &logEntry); err == nil {
				if tsStr, ok := logEntry["ts"].(string); ok {
					if parsed, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
						ts = parsed
					}
				}
			}

			// Calculate content hash for deduplication
			hash := fmt.Sprintf("%x", md5.Sum([]byte(line)))

			logChan <- &logLine{
				raw:       line,
				timestamp: ts,
				hash:      hash,
				line:      formatLogLine(line),
			}
		}
	}

	return scanner.Err()
}

// logFilter holds criteria for filtering log lines.
type logFilter struct {
	planName     string
	target       string
	executionIDs []string
	level        string
}

func buildLogFilter(planName string, opts *logsOptions, plan *hibernatorv1alpha1.HibernatePlan) *logFilter {
	f := &logFilter{
		planName: planName,
		target:   opts.target,
		level:    strings.ToLower(opts.level),
	}

	// Collect execution IDs from current executions
	for _, exec := range plan.Status.Executions {
		if exec.LogsRef != "" {
			f.executionIDs = append(f.executionIDs, strings.TrimPrefix(exec.LogsRef, wellknown.ExecutionIDLogPrefix))
		}
	}

	return f
}

// matches checks if a log line matches the filter criteria.
func (f *logFilter) matches(line string) bool {
	if !strings.Contains(line, "execution-service.runner-logs") {
		return false
	}

	// Must contain the plan name
	if !strings.Contains(line, f.planName) {
		// Also check for execution IDs if plan name not found directly
		foundID := false
		for _, id := range f.executionIDs {
			if strings.Contains(line, id) {
				foundID = true
				break
			}
		}
		if !foundID {
			return false
		}
	}

	// Optional target filter
	if f.target != "" && !strings.Contains(line, f.target) {
		return false
	}

	// Optional level filter
	// Note: Runner logs are all INFO level, so we detect "error" by presence of error field
	if f.level != "" {
		var logEntry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &logEntry); err == nil {
			switch f.level {
			case "error":
				// Error logs have an error field present and non-empty
				errField := extractString(logEntry, "error")
				if errField == "" {
					return false
				}
			case "info":
				// Info logs (default) should NOT have an error field
				errField := extractString(logEntry, "error")
				if errField != "" {
					return false
				}
				// Add more level types as needed (debug, warn, etc.)
			}
		}
	}

	return true
}

// formatLogLine attempts to pretty-print a structured JSON log line.
// Handles different log types (startup, progress, error, waiting, polling, operation).
// Falls back to returning the raw line if parsing fails.
func formatLogLine(line string) string {
	// Try to parse as JSON
	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(line), &logEntry); err != nil {
		return line
	}

	// Extract common fields
	ts := extractTimestamp(logEntry)
	level := extractString(logEntry, "level")
	msg := extractString(logEntry, "msg")
	target := extractString(logEntry, "target")
	execID := extractString(logEntry, "executionId")

	// Build base log line
	var sb strings.Builder

	// Timestamp
	if ts != "" {
		sb.WriteString(ts)
		sb.WriteString(" ")
	}

	// Log level (highlight errors)
	if level != "" {
		sb.WriteString("[")
		if level == "error" || extractString(logEntry, "error") != "" {
			sb.WriteString("ERROR")
		} else {
			sb.WriteString(strings.ToUpper(level))
		}
		sb.WriteString("] ")
	}

	// Execution context
	if execID != "" && target != "" {
		sb.WriteString(fmt.Sprintf("(exec=%s, target=%s) ", execID, target))
	}

	// Main message
	sb.WriteString(msg)

	// Handle different log types
	switch msg {
	case "starting runner":
		if op := extractString(logEntry, "operation"); op != "" {
			sb.WriteString(fmt.Sprintf(" [%s]", op))
		}
		if tt := extractString(logEntry, "targetType"); tt != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", tt))
		}

	case "progress":
		if message := extractString(logEntry, "message"); message != "" {
			sb.WriteString(fmt.Sprintf(": %s", message))
		}
		if phase := extractString(logEntry, "phase"); phase != "" {
			sb.WriteString(fmt.Sprintf(" (phase: %s", phase))
			if percent := extractString(logEntry, "percent"); percent != "" {
				sb.WriteString(fmt.Sprintf(", %s%%", percent))
			}
			sb.WriteString(")")
		}

	case "error context":
		if errMsg := extractString(logEntry, "error"); errMsg != "" {
			sb.WriteString(fmt.Sprintf(": %s", errMsg))
		}

	case "waiting for workload replicas to scale", "waiting for operation":
		if name := extractString(logEntry, "name"); name != "" {
			ns := extractString(logEntry, "namespace")
			sb.WriteString(fmt.Sprintf(" %s/%s", ns, name))
		}
		if desc := extractString(logEntry, "description"); desc != "" {
			sb.WriteString(fmt.Sprintf(": %s", desc))
		}
		if timeout := extractString(logEntry, "timeout"); timeout != "" {
			sb.WriteString(fmt.Sprintf(" (timeout: %s)", timeout))
		}

	case "polling operation (initial)", "polling operation":
		if desc := extractString(logEntry, "description"); desc != "" {
			sb.WriteString(fmt.Sprintf(": %s", desc))
		}
		if status := extractString(logEntry, "status"); status != "" {
			sb.WriteString(fmt.Sprintf(" → %s", status))
		}

	case "operation completed":
		if desc := extractString(logEntry, "description"); desc != "" {
			sb.WriteString(fmt.Sprintf(": %s", desc))
		}

	default:
		// Generic handling for other message types
		if desc := extractString(logEntry, "description"); desc != "" {
			sb.WriteString(fmt.Sprintf(": %s", desc))
		}
		if message := extractString(logEntry, "message"); message != "" {
			sb.WriteString(fmt.Sprintf(": %s", message))
		}
		if status := extractString(logEntry, "status"); status != "" {
			sb.WriteString(fmt.Sprintf(" → %s", status))
		}
		if errMsg := extractString(logEntry, "error"); errMsg != "" {
			sb.WriteString(fmt.Sprintf(" (ERROR: %s)", errMsg))
		}
	}

	return sb.String()
}

// extractTimestamp extracts and formats the timestamp from log entry.
// Prefers "ts" field (RFC3339Nano), falls back to "timestamp" field.
// Converts to local timezone for display.
func extractTimestamp(logEntry map[string]interface{}) string {
	// Try "ts" field first (standard logr field)
	if tsStr, ok := logEntry["ts"].(string); ok && tsStr != "" {
		// Parse RFC3339Nano and format as readable timestamp in local timezone
		if parsed, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
			return parsed.Local().Format("2006-01-02T15:04:05")
		}
		return tsStr
	}

	// Fall back to "timestamp" field
	if tsStr, ok := logEntry["timestamp"].(string); ok && tsStr != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
			return parsed.Local().Format("2006-01-02T15:04:05")
		}
		return tsStr
	}

	return ""
}

// extractString safely extracts a string value from JSON map, handling various types.
func extractString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		if f, ok := v.(float64); ok {
			return fmt.Sprintf("%.0f", f)
		}
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// discoverControllerNamespace returns the namespace where the controller is expected to run.
// Defaults to "hibernator-system" if not overridden.
func discoverControllerNamespace() string {
	if ns := os.Getenv("HIBERNATOR_CONTROLLER_NAMESPACE"); ns != "" {
		return ns
	}
	return "hibernator-system"
}
