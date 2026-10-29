---
name: access-log-triage
description: 'Read what actually reached a Datum load balancer from its access logs. Use when status reports nothing wrong and requests still fail, or to check whether it is serving at all.'
---

# access-log-triage

Networking publishes this procedure through datum-mcp, so it is always the
version that matches the tools. Load it and follow it:

1. Call `alb_guide` with `skill: "access-log-triage"`.
2. Follow the document it returns. Its preamble says how the steps map onto
   datum-mcp's tools, including how to preview a change and get a yes before
   making it.

If `alb_guide` is not listed, load the `alb` toolset with the `toolsets`
tool. datum-mcp comes with the `datum-cloud` plugin; if it is not connected at
all, tell the person to install datum-mcp
(`brew install datum-cloud/tap/datum-mcp`) and restart Claude Code.
