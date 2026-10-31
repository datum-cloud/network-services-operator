---
name: network-not-ready
description: 'Fix a Datum network that is not ready, or a workload that says its network is not ready.'
---

# network-not-ready

Networking publishes this procedure through datum-mcp, so it is always the
version that matches the tools. Load it and follow it:

1. Call `network_guide` with `skill: "network-not-ready"`.
2. Follow the document it returns. Its preamble says how the steps map onto
   datum-mcp's tools, including how to preview a change and get a yes before
   making it.

If `network_guide` is not listed, load the `network` toolset with the `toolsets`
tool. datum-mcp comes with the `datum-cloud` plugin; if it is not connected at
all, tell the person to install datum-mcp
(`brew install datum-cloud/tap/datum-mcp`) and restart Claude Code.
