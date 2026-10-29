---
name: network-service-create
description: 'Create a Datum network service, the stable name a load balancer uses to reach a workload''s instances. Use when someone asks for a "service" on a network.'
---

# network-service-create

Networking publishes this procedure through datum-mcp, so it is always the
version that matches the tools. Load it and follow it:

1. Call `network_guide` with `skill: "network-service-create"`.
2. Follow the document it returns. Its preamble says how the steps map onto
   datum-mcp's tools, including how to preview a change and get a yes before
   making it.

If `network_guide` is not listed, load the `network` toolset with the `toolsets`
tool. If datum-mcp is not connected at all, tell the person to install it
(`brew install datum-cloud/tap/datum-mcp`) and restart Claude Code.
