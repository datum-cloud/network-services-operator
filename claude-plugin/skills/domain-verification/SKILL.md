---
name: domain-verification
description: 'Prove ownership of a domain for a Datum load balancer hostname. Use when a hostname''s ownership-verified step is not ok, or Datum reports a domain as not verified.'
---

# domain-verification

Networking publishes this procedure through datum-mcp, so it is always the
version that matches the tools. Load it and follow it:

1. Call `alb_guide` with `skill: "domain-verification"`.
2. Follow the document it returns. Its preamble says how the steps map onto
   datum-mcp's tools, including how to preview a change and get a yes before
   making it.

If `alb_guide` is not listed, load the `alb` toolset with the `toolsets`
tool. If datum-mcp is not connected at all, tell the person to install it
(`brew install datum-cloud/tap/datum-mcp`) and restart Claude Code.
