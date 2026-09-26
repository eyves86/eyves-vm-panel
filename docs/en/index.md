---
layout: home

hero:
  name: EyvesCloud
  text: Enterprise Multi-Node Virtualization Platform
  tagline: Manage cross-node LXC / KVM workloads from a unified control plane — scheduling engine, multi-tenant isolation, metering integration, and security auditing, out of the box.
  actions:
    - theme: brand
      text: Get Started
      link: /en/guide/installation
    - theme: alt
      text: View API
      link: /en/features/api
    - theme: alt
      text: Capabilities
      link: /en/features/containers

features:
  - title: Unified Control Plane
    details: One controller manages unlimited worker nodes with a consistent web console, CLI, and versioned REST API — no more logging into each host.
  - title: Intelligent Scheduling
    details: Filtering (online/capacity/storage backend/maintenance) → scoring (RAM + disk + tenant spread) → decision trail, fully diagnosable.
  - title: Enterprise Multi-Tenancy
    details: Sub-users with operator/viewer roles, per-container authorization, tenant quotas, dedicated portal entry, and token version control.
  - title: Metering & Billing Ready
    details: Full usage export API (resource config + live usage + expiry/traffic), plus idempotent provisioning for seamless billing integration.
  - title: Defense in Depth
    details: Conntrack-based threat detection, JWT issuer/audience binding, fine-grained API key scopes, and full operation & login auditing.
  - title: Smooth Operations
    details: Node maintenance mode (drain/evacuate), proactive health probing with alerts, in-panel self-update (selectable repo and version), snapshots, and automatic expiry reclamation.
---
