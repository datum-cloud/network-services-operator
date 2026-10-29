---
name: alb-create
description: 'Create a Datum Cloud Application Load Balancer, or add a hostname, route or origin to one. Use when someone wants to put a site or API behind Datum, and before any change to an existing load balancer.'
---

# alb-create

Networking publishes this procedure through datum-mcp, so it is always the
version that matches the tools. Load it and follow it:

1. Call `alb_guide` with `skill: "alb-create"`.
2. Follow the document it returns. Its preamble says how the steps map onto
   datum-mcp's tools, including how to preview a change and get a yes before
   making it.

If `alb_guide` is not listed, load the `alb` toolset with the `toolsets`
tool. If datum-mcp is not connected at all, tell the person to install it
(`brew install datum-cloud/tap/datum-mcp`) and restart Claude Code.
