---
name: certificate-not-issued
description: 'Fix a TLS certificate that Datum has not issued for a load balancer hostname. Use when HTTPS fails on one hostname while HTTP works, or its certificate step is not ok.'
---

# certificate-not-issued

Networking publishes this procedure through datum-mcp, so it is always the
version that matches the tools. Load it and follow it:

1. Call `alb_guide` with `skill: "certificate-not-issued"`.
2. Follow the document it returns. Its preamble says how the steps map onto
   datum-mcp's tools, including how to preview a change and get a yes before
   making it.

If `alb_guide` is not listed, load the `alb` toolset with the `toolsets`
tool. datum-mcp comes with the `datum-cloud` plugin; if it is not connected at
all, tell the person to install datum-mcp
(`brew install datum-cloud/tap/datum-mcp`) and restart Claude Code.
