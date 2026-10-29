---
name: hostname-not-working
description: 'Fix one hostname on a Datum load balancer that does not work while others do, or a newly attached custom hostname that has never worked.'
---

# hostname-not-working

Networking publishes this procedure through datum-mcp, so it is always the
version that matches the tools. Load it and follow it:

1. Call `alb_guide` with `skill: "hostname-not-working"`.
2. Follow the document it returns. Its preamble says how the steps map onto
   datum-mcp's tools, including how to preview a change and get a yes before
   making it.

If `alb_guide` is not listed, load the `alb` toolset with the `toolsets`
tool. datum-mcp comes with the `datum-cloud` plugin; if it is not connected at
all, tell the person to install datum-mcp
(`brew install datum-cloud/tap/datum-mcp`) and restart Claude Code.
