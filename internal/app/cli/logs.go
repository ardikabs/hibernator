/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

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
	root     *rootOptions
	executor string
	target   string
	tail     int64
	follow   bool
}

// newLogsCommand creates the "logs" command.
func newLogsCommand(opts *rootOptions) *cobra.Command {
	logsOpts := &logsOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "logs <plan-name>",
		Short: "View controller logs filtered by plan context",
		Long: `Fetch logs from the Hibernator controller pod and filter them
by plan name, executor, target, or execution ID.

The command discovers the controller pod automatically by label selector
(app.kubernetes.io/name=hibernator), then streams or tails its logs.

Examples:
  kubectl hibernator logs my-plan
  kubectl hibernator logs my-plan --tail 100
  kubectl hibernator logs my-plan --executor eks --target my-cluster
  kubectl hibernator logs my-plan --follow
  kubectl hibernator logs my-plan --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(cmd.Context(), logsOpts, args[0])
		},
	}

	cmd.Flags().StringVar(&logsOpts.executor, "executor", "", "Filter logs by executor type (e.g., eks, rds, ec2)")
	cmd.Flags().StringVar(&logsOpts.target, "target", "", "Filter logs by target name")
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

	// Discover controller pod
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

	// Use the first running pod
	var controllerPod *corev1.Pod
	for i := range podList.Items {
		if podList.Items[i].Status.Phase == corev1.PodRunning {
			controllerPod = &podList.Items[i]
			break
		}
	}
	if controllerPod == nil {
		controllerPod = &podList.Items[0]
	}

	// Build clientset for pod logs API
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	// Fetch logs
	logOpts := &corev1.PodLogOptions{
		Follow: opts.follow,
	}
	if opts.tail > 0 {
		logOpts.TailLines = &opts.tail
	}

	req := clientset.CoreV1().Pods(controllerPod.Namespace).GetLogs(controllerPod.Name, logOpts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("failed to stream logs from pod %q: %w", controllerPod.Name, err)
	}
	defer stream.Close()

	// Build filter context from plan
	filter := buildLogFilter(planName, opts, &plan)

	// Parse and filter logs
	scanner := bufio.NewScanner(stream)
	// Increase buffer for long log lines
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		fmt.Println(line)

		if filter.matches(line) {
			if opts.root.jsonOutput {
				// Pass through JSON lines as-is
				fmt.Println(line)
			} else {
				fmt.Println(formatLogLine(line))
			}
		}
	}

	return scanner.Err()
}

// logFilter holds criteria for filtering log lines.
type logFilter struct {
	planName     string
	executor     string
	target       string
	executionIDs []string
}

func buildLogFilter(planName string, opts *logsOptions, plan *hibernatorv1alpha1.HibernatePlan) *logFilter {
	f := &logFilter{
		planName: planName,
		executor: opts.executor,
		target:   opts.target,
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

	// Optional executor filter
	if f.executor != "" && !strings.Contains(line, f.executor) {
		return false
	}

	// Optional target filter
	if f.target != "" && !strings.Contains(line, f.target) {
		return false
	}

	return true
}

// formatLogLine attempts to pretty-print a structured JSON log line.
// Falls back to returning the raw line if parsing fails.
func formatLogLine(line string) string {
	// Try to parse as JSON
	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(line), &logEntry); err != nil {
		return line
	}

	// Extract common fields
	ts, _ := logEntry["ts"].(string)
	if ts == "" {
		if tsFloat, ok := logEntry["ts"].(float64); ok {
			ts = fmt.Sprintf("%.3f", tsFloat)
		}
	}
	level, _ := logEntry["level"].(string)
	msg, _ := logEntry["msg"].(string)
	logger, _ := logEntry["logger"].(string)

	// Check for execution-id prefix
	execID := ""
	if eid, ok := logEntry["execution-id"].(string); ok {
		execID = eid
	}

	var sb strings.Builder
	if ts != "" {
		sb.WriteString(ts)
		sb.WriteString(" ")
	}
	if level != "" {
		sb.WriteString("[")
		sb.WriteString(strings.ToUpper(level))
		sb.WriteString("] ")
	}
	if logger != "" {
		sb.WriteString(logger)
		sb.WriteString(": ")
	}
	if execID != "" {
		sb.WriteString(wellknown.ExecutionIDLogPrefix)
		sb.WriteString(execID)
		sb.WriteString(" ")
	}
	sb.WriteString(msg)

	return sb.String()
}

// discoverControllerNamespace returns the namespace where the controller is expected to run.
// Defaults to "hibernator-system" if not overridden.
func discoverControllerNamespace() string {
	if ns := os.Getenv("HIBERNATOR_CONTROLLER_NAMESPACE"); ns != "" {
		return ns
	}
	return "hibernator-system"
}
