---
name: network-service-not-serving
description: 'Fix a Datum network service that sends traffic nowhere: not ready, "no endpoints", or a load balancer pointing at it returns origin errors.'
---

# network-service-not-serving

Networking publishes this procedure through datum-mcp, so it is always the
version that matches the tools. Load it and follow it:

1. Call `network_guide` with `skill: "network-service-not-serving"`.
2. Follow the document it returns. Its preamble says how the steps map onto
   datum-mcp's tools, including how to preview a change and get a yes before
   making it.

If `network_guide` is not listed, load the `network` toolset with the `toolsets`
tool. datum-mcp comes with the `datum-cloud` plugin; if it is not connected at
all, tell the person to install datum-mcp
(`brew install datum-cloud/tap/datum-mcp`) and restart Claude Code.
