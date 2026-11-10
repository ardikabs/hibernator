/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"github.com/spf13/cobra"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
)

type restorePointOptions struct {
	root       *common.RootOptions
	target     string
	resourceID string
	dryRun     bool
}

// NewCommand creates the "restore" parent command group.
func NewCommand(opts *common.RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Manage restore points for HibernatePlans",
		Long: `Commands for inspecting and managing restore points (captured resource state during hibernation).

Restore points store metadata about resources as they were when hibernation occurred,
enabling proper restoration during wakeup with correct configuration and state.

Restore data is stored in a ConfigMap named hibernator-restore-<plan-name>.

Understanding Restore Point Timestamps:

Restore point data contains two important timestamps that serve different purposes:

- **Captured At**: The timestamp when the hibernator captured and initiated the save operation to the ConfigMap. This tracks when the data was captured from the hibernator's perspective.
- **Reported At**: The time when the executor callback was triggered for a specific resource (via ReportStateCallback). This tracks when each resource's state was initially reported during the shutdown process.

Key Differences:

- **Captured At** is set per-target when the hibernator initiates the ConfigMap update. It serves as a historical marker for auditing and freshness checks.
- **Reported At** is set per-resource when the executor reports the state (via callback) during the shutdown process. Multiple resources may be reported at different times within the same hibernation cycle.
- **Reported At** is used for idempotency tracking — it helps determine if a resource has already been reported in the current hibernation cycle.
- **Captured At** tracks when the hibernator captured the data, useful for historical analysis and determining data freshness.

#### Example Flow

##### Hibernation Cycle

1. Executor discovers resource A → Reports state → Reported At = T1
2. Executor discovers resource B → Reports state → Reported At = T2
3. Hibernator captures and initiates save to ConfigMap → Captured At = T3

##### Weekend partial runs

ScheduleException target overrides disable whole targets; when only some resources within one target should run over the weekend, trim the restore data instead.`,
		Example: `# Friday evening: snapshot the full restore data
kubectl hibernator restore export weekend-plan -t eks --file eks-full.json

# Trim to weekend runners only
kubectl hibernator restore prune weekend-plan -t eks --keep ng-weekend-a --keep ng-weekend-b --yes

# ... SAT/SUN partial runs happen ...

# Monday: restore the full snapshot
kubectl hibernator restore import weekend-plan -t eks --file eks-full.json --yes

# Initialize empty restore point for target
kubectl hibernator restore init my-plan --target eks-cluster --executor eks

# Show restore point summary (default)
kubectl hibernator restore list my-plan

# List all resources (detailed view)
kubectl hibernator restore list my-plan -o wide

# List all resources for a specific target
kubectl hibernator restore list my-plan --target eks-cluster -o wide

# Inspect of a specific resource in the restore point
kubectl hibernator restore inspect my-plan --target eks-cluster --resource-id xyz

# Update
kubectl hibernator restore patch my-plan --target eks-cluster --resource-id xyz --set desiredCapacity=10

# Drop a resource from the restore point (use with caution)
kubectl hibernator restore drop my-plan --target eks-cluster --resource-id xyz

# Back up a target entry, prune to weekend resources, restore afterwards
kubectl hibernator restore export my-plan --target eks-cluster --file eks-full.json
# Trim the target down to weekend resources
kubectl hibernator restore prune my-plan --target eks-cluster --keep ng-weekend --yes
# Restore the full target entry from backup
kubectl hibernator restore import my-plan --target eks-cluster --file eks-full.json --yes

# Back up and restore a single resource state
kubectl hibernator restore export my-plan --target eks-cluster --resource-id xyz --file xyz.json
# Restore a single resource state from backup
kubectl hibernator restore import my-plan --target eks-cluster --resource-id xyz --file xyz.json --yes`,
	}

	cmd.AddCommand(newInitCommand(opts))
	cmd.AddCommand(newListCommand(opts))
	cmd.AddCommand(newInspectCommand(opts))
	cmd.AddCommand(newPatchCommand(opts))
	cmd.AddCommand(newDropCommand(opts))
	cmd.AddCommand(newExportCommand(opts))
	cmd.AddCommand(newImportCommand(opts))
	cmd.AddCommand(newPruneCommand(opts))

	return cmd
}
