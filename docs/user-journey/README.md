# User Journey Documentation

This directory contains detailed user journey documentation for the Hibernator Operator, organized by business outcomes and prioritized by implementation tier. Each journey guides users through real-world scenarios with step-by-step flows, decision branches, and RFC references.

## What is a User Journey?

A user journey maps a specific business goal to concrete steps a user takes to accomplish it. Each journey is framed as a **user story** in the agile format: "As a [persona], I want to [action], so that [benefit]."

Each journey identifies:
- **Who** (personas involved)
- **What** (specific action and business outcome)
- **When** (trigger/scenario/context)
- **Why** (business value and pain points solved)
- **How** (step-by-step flow with decisions, code examples, and verification)

Journeys cross multiple RFCs and represent the user's perspective, not technical implementation details.

## How to Use This Guide

1. **Find your role**: Look for your persona in the [Personas Reference](#personas-reference) section below.
2. **Identify your goal**: Search for the business outcome you're trying to achieve.
3. **Follow the flow**: Each journey provides step-by-step guidance with examples, decision points, and related journeys.
4. **Check RFC context**: Each journey links to relevant RFCs for deeper technical background.
5. **Check status**: Each journey shows its implementation status (see legend below).

---

## Status Legend

Journey statuses indicate the current state of implementation in the Hibernator Operator:

- **✅ Implemented** — Feature is complete and production-ready
- **🚀 In Progress** — Feature is actively being built (MVP Phase)
- **📋 Planned** — Feature is scheduled for development (P1/P2/P3)
- **⏳ Proposed** — Feature design documented but not yet scheduled
- **🔧 Under Maintenance** — Feature exists but is being improved or stabilized
- **❌ Obsolete** — Feature is deprecated or no longer supported

---

## Personas Reference

This section lists all personas involved in Hibernator user journeys. Each persona represents a distinct role with specific goals and workflows.

| Persona | Role & Responsibility | Involved Journeys | Status |
|---------|----------------------|-------------------|--------|
| **Platform Engineer** | Designs hibernation strategy, creates plans, defines execution logic | 11 journeys (all tiers) | ✅ Core persona |
| **DevOps Engineer** | Deploys operator, configures auth (IRSA, RBAC), integrates with GitOps | 9 journeys (MVP, Enhanced, Advanced) | ✅ Core persona |
| **SRE / On-Call Engineer** | Monitors execution, troubleshoots failures, responds to incidents | 7 journeys (MVP, Enhanced) | ✅ Core persona |
| **Cloud Administrator** | Sets up cloud credentials, configures cross-account access, audit trails | 6 journeys (MVP, Enhanced, Advanced) | ✅ Core persona |
| **Cluster Operator** | Manages Kubernetes cluster connectors, RBAC, cluster access | 4 journeys (MVP, Enhanced) | ✅ Core persona |
| **DevOps Engineer (Automation)** | Sets up CI/CD integration, automated approvals, GitOps pipelines | 3 journeys (Enhanced, Advanced) | ✅ Secondary persona |
| **Engineering Manager / Engineering Head** | Reviews and approves exceptions, governs access, maintains policies | 4 journeys (Enhanced, Advanced) | ✅ Secondary persona |
| **Team Lead / Manager** | Requests exceptions for events, manages team schedules, cost tracking | 4 journeys (Enhanced, Advanced) | ✅ Secondary persona |
| **End User / Application Team** | Discovers hibernation impact, understands schedule, handles wakeup | 2 journeys (MVP, Enhanced) | ✅ Secondary persona |
| **Security Officer / Compliance Officer** | Enforces RBAC, audits operations, maintains compliance trails | 4 journeys (Enhanced, Advanced) | ✅ Security persona |
| **Incident Commander** | Orchestrates emergency exceptions, coordinates wakeup during incidents | 2 journeys (Enhanced) | ✅ Secondary persona |
| **Auditor** | Reviews audit logs, detects anomalies, maintains compliance records | 1 journey (Advanced) | ✅ Specialized persona |
| **Enterprise Administrator** | Manages multi-tenant deployments, enforces org governance | 1 journey (Advanced) | ✅ Specialized persona |
| **Cost Manager / Finance Manager** | Tracks cost savings, monitors ROI of hibernation | 2 journeys (Enhanced, Advanced) | ⏳ Emerging persona |
| **Product Manager / Project Manager** | Plans events/projects, coordinates hibernation timing with business | 2 journeys (Enhanced) | ⏳ Emerging persona |
| **Release Manager** | Controls prod rollouts, manages promotion gates, deployment orchestration | 1 journey (Enhanced) | ⏳ Emerging persona |

---

## Journey Index

All journeys listed below are organized by priority tier. Each journey may reference multiple RFCs, and each RFC appears in multiple journeys (see [RFC Legend](#rfc-legend) below).

### MVP Tier — Core Hibernation Workflows (11 journeys)

Core features that are production-ready and actively used.

| Journey | Business Outcome | Personas | Status | RFCs | Link |
|---------|------------------|----------|--------|------|------|
| **Hibernation Plan Initial Design** | Design and deploy a declarative hibernation strategy for cloud resources | Platform Engineer, DevOps | ✅ Implemented | RFC-0001, RFC-0002, RFC-0004 | [Details](./hibernation-plan-initial-design.md) |
| **EKS Managed NodeGroup Hibernation** | Reduce costs by scaling EKS managed node groups to zero during off-hours | Platform Engineer, SRE | ✅ Implemented | RFC-0001 | [Details](./eks-managed-nodegroup-hibernation.md) |
| **RDS Database Hibernation** | Stop RDS databases during off-hours with optional snapshots before stopping | Platform Engineer, SRE | ✅ Implemented | RFC-0001 | [Details](./rds-database-hibernation.md) |
| **EC2 Instance Hibernation** | Stop EC2 instances using tag-based selection during off-hours | Platform Engineer, SRE | ✅ Implemented | RFC-0001 | [Details](./ec2-instance-hibernation.md) |
| **Monitor Hibernation Execution** | Observe hibernation progress, status, and logs in real-time | SRE, On-Call Engineer | ✅ Implemented | RFC-0001 | [Details](./monitor-hibernation-execution.md) |
| **Troubleshoot Hibernation Failure** | Debug and resolve hibernation execution errors | SRE, Platform Engineer | ✅ Implemented | RFC-0001 | [Details](./troubleshoot-hibernation-failure.md) |
| **Deploy Operator to Cluster** | Install and configure Hibernator Operator on a Kubernetes cluster | DevOps Engineer | ✅ Implemented | RFC-0001 | [Details](./deploy-operator-to-cluster.md) |
| **Create CloudProvider Connector** | Configure cloud credentials (AWS, GCP, Azure) for resource access | Cloud Administrator, DevOps | ✅ Implemented | RFC-0001 | [Details](./create-cloudprovider-connector.md) |
| **Create K8SCluster Connector** | Configure Kubernetes cluster access (EKS, GKE, on-prem) | Cluster Operator, DevOps | ✅ Implemented | RFC-0001 | [Details](./create-k8scluster-connector.md) |
| **Wakeup and Restore Resources** | Automatically restore resources from hibernation to normal operation | SRE, Platform Engineer | ✅ Implemented | RFC-0001 | [Details](./wakeup-and-restore-resources.md) |
| **Discover Hibernation Impact** | Understand how hibernation affects application deployments | End User, Application Team | ✅ Implemented | RFC-0002 | [Details](./discover-hibernation-impact.md) |

### Enhanced Tier — Exceptions, Approvals, Workloads (9 journeys)

Advanced workflows for exception handling, governance, and multi-environment management.

| Journey | Business Outcome | Personas | Status | RFCs | Link |
|---------|------------------|----------|--------|------|------|
| **Create Emergency Exception** | Temporarily override hibernation schedule for incidents or urgent changes | On-Call Engineer, Platform Engineer | 🚀 In Progress | RFC-0003 | [Details](./create-emergency-exception.md) |
| **Approve Hibernation Exceptions** | Review and approve exceptions via Slack (interactive) or CLI (programmatic) | Engineering Manager, Engineering Head, Security Officer | 📋 Planned | RFC-0003 | [Details](./approve-hibernation-exceptions.md) |
| **Scale Workloads in Cluster** | Downscale in-cluster workloads (Deployments, StatefulSets) during hibernation | Platform Engineer, SRE | ✅ Implemented | RFC-0004 | [Details](./scale-workloads-in-cluster.md) |
| **Extend Hibernation for Event** | Temporarily extend hibernation schedule for on-site events or special projects | Team Lead, Product Manager | 📋 Planned | RFC-0003 | [Details](./extend-hibernation-for-event.md) |
| **Suspend Hibernation During Incident** | Create a carve-out from hibernation to keep services awake during incidents | On-Call Engineer, SRE | 📋 Planned | RFC-0003 | [Details](./suspend-hibernation-during-incident.md) |
| **Setup IRSA Authentication** | Use IAM Roles for Service Accounts (IRSA) for secure AWS credential access | Cloud Administrator, DevOps | ✅ Implemented | RFC-0001 | [Details](./setup-irsa-authentication.md) |
| **Configure RBAC for Hibernation** | Set up Kubernetes RBAC to control who can create/manage hibernation plans | DevOps Engineer, Cluster Operator | ✅ Implemented | RFC-0001 | [Details](./configure-rbac-for-hibernation.md) |
| **Integrate with GitOps** | Add HibernatePlans to version-controlled infrastructure-as-code pipelines | DevOps Engineer, Platform Engineer | 📋 Planned | RFC-0001, RFC-0002 | [Details](./integrate-with-gitops.md) |
| **Manage Multi-Environment Schedules** | Create different hibernation policies for DEV, STG, and PROD environments | Platform Engineer, Team Lead | 🔧 Under Maintenance | RFC-0001, RFC-0002 | [Details](./manage-multi-environment-schedules.md) |

### Advanced Tier — Multi-Cloud, Admin, Cross-Organization (5 journeys)

Enterprise-scale deployments with compliance, audit, and multi-tenant support.

| Journey | Business Outcome | Personas | Status | RFCs | Link |
|---------|------------------|----------|--------|------|------|
| **Setup Cross-Account Hibernation** | Allow prod account to manage hibernation of resources in dev/staging accounts | Cloud Administrator, Platform Engineer | 📋 Planned | RFC-0001 | [Details](./setup-cross-account-hibernation.md) |
| **Audit Hibernation Operations** | Create compliance trails for all hibernation operations via CloudTrail and K8S audit logs | Security Officer, Compliance Officer | ⏳ Proposed | RFC-0001 | [Details](./audit-hibernation-operations.md) |
| **Manage Runner Streaming Configuration** | Configure gRPC or webhook streaming for real-time execution logs and progress | DevOps Engineer, SRE | 🚀 In Progress | RFC-0001 | [Details](./manage-runner-streaming-config.md) |
| **Scale Hibernation Across Organizations** | Extend hibernation operator to support multiple organizations/teams safely | Enterprise Administrator, Platform Engineer | ⏳ Proposed | RFC-0001, RFC-0003 | [Details](./scale-hibernation-across-organizations.md) |
| **Migrate from CronJob to Schedules** | Transition from ad-hoc scripts/CronJobs to declarative HibernatePlans | Platform Engineer, DevOps | 📋 Planned | RFC-0001, RFC-0002 | [Details](./migrate-from-cronjob-to-schedules.md) |

---

## RFC Legend

Each RFC covers multiple user journeys. Use this legend to find all journeys related to a specific RFC:

### RFC-0001: Control Plane + Runner Model
**Core architecture, execution strategies, authentication, and observability**

Covers journeys:
- [Hibernation Plan Initial Design](./hibernation-plan-initial-design.md)
- [EKS Managed NodeGroup Hibernation](./eks-managed-nodegroup-hibernation.md)
- [RDS Database Hibernation](./rds-database-hibernation.md)
- [EC2 Instance Hibernation](./ec2-instance-hibernation.md)
- [Monitor Hibernation Execution](./monitor-hibernation-execution.md)
- [Troubleshoot Hibernation Failure](./troubleshoot-hibernation-failure.md)
- [Deploy Operator to Cluster](./deploy-operator-to-cluster.md)
- [Create CloudProvider Connector](./create-cloudprovider-connector.md)
- [Create K8SCluster Connector](./create-k8scluster-connector.md)
- [Wakeup and Restore Resources](./wakeup-and-restore-resources.md)
- [Setup IRSA Authentication](./setup-irsa-authentication.md)
- [Configure RBAC for Hibernation](./configure-rbac-for-hibernation.md)
- [Integrate with GitOps](./integrate-with-gitops.md)
- [Manage Multi-Environment Schedules](./manage-multi-environment-schedules.md)
- [Setup Cross-Account Hibernation](./setup-cross-account-hibernation.md)
- [Audit Hibernation Operations](./audit-hibernation-operations.md)
- [Manage Runner Streaming Configuration](./manage-runner-streaming-config.md)
- [Scale Hibernation Across Organizations](./scale-hibernation-across-organizations.md)
- [Migrate from CronJob to Schedules](./migrate-from-cronjob-to-schedules.md)

### RFC-0002: User-Friendly Schedule Format Migration
**Readable schedule formats (start/end/daysOfWeek), timezone support, validation**

Covers journeys:
- [Hibernation Plan Initial Design](./hibernation-plan-initial-design.md)
- [Discover Hibernation Impact](./discover-hibernation-impact.md)
- [Integrate with GitOps](./integrate-with-gitops.md)
- [Manage Multi-Environment Schedules](./manage-multi-environment-schedules.md)
- [Migrate from CronJob to Schedules](./migrate-from-cronjob-to-schedules.md)

### RFC-0003: Temporary Schedule Exceptions and Overrides
**Exception types (suspend/extend/replace), approval workflows, lead time, auto-expiration**

Covers journeys:
- [Create Emergency Exception](./create-emergency-exception.md)
- [Approve Hibernation Exceptions](./approve-hibernation-exceptions.md)
- [Extend Hibernation for Event](./extend-hibernation-for-event.md)
- [Suspend Hibernation During Incident](./suspend-hibernation-during-incident.md)
- [Scale Hibernation Across Organizations](./scale-hibernation-across-organizations.md)

### RFC-0004: Scale Subresource Executor for Workload Downscaling
**Generic workload scaling, namespace scoping, replica restoration**

Covers journeys:
- [Hibernation Plan Initial Design](./hibernation-plan-initial-design.md)
- [Scale Workloads in Cluster](./scale-workloads-in-cluster.md)

---

## Feedback & Contributions

Found an issue or have suggestions? [Open an issue](https://github.com/ardikasaputro/hibernator/issues) or submit a pull request with improvements to these journeys.

---

**Last Updated**: February 2026
**Total Journeys**: 26 (11 MVP + 10 Enhanced + 5 Advanced)
