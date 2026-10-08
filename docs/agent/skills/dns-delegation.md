# Skill: who answers DNS for this domain

Use when a cause is `DNSAuthorityMissing`, `DNSZoneNotReady`,
`DNSZoneNameserverMismatch`, or when a DNS-record step reads `notApplicable`.

## The question underneath all of these

Does Datum answer DNS for this domain, or does the customer?

Both are supported and neither is wrong. Almost every one of these causes is
really a mismatch between the two — the customer set things up expecting one
arrangement, and the domain is actually in the other.

## They run their own DNS

The DNS-record step reads `notApplicable`, with reason `DNSZoneNotFound` or
`NotApplicable`. Both are reported as true: it means "not ours to write", not
"failed".

Nothing is pending. The customer creates one record with their own provider:

> Create a CNAME from `app.example.com` to `<generated hostname>`.

That is the whole answer. Do not tell them to wait.

At a zone apex (`example.com` with no subdomain) a CNAME is not allowed by DNS
itself. Their provider may offer ALIAS, ANAME or flattening; if it does not, the
options are to use a subdomain, or to move the domain's DNS to Datum.

## Datum should run its DNS, but does not yet

`DNSAuthorityMissing` means ownership is proven but Datum is not being asked the
questions, so it cannot create the record. `DNSZoneNameserverMismatch` says the
same thing from the domain's side.

`alb_diagnose` returns the domain's current `nameservers`. Compare them with the
ones Datum's zone publishes. The fix is at the **registrar**, not in the DNS
zone: the customer changes the domain's nameservers to Datum's.

Two things to say plainly:

- This moves **all** DNS for the domain, not just this hostname. Anything else
  answered from the old provider needs to exist in the new zone first, or it
  stops resolving. This is the part that causes outages.
- Nameserver changes propagate on the registry's schedule, which can be slow.

If they would rather not move the domain, the alternative is the arrangement
above: keep their own DNS and create a CNAME.

## Datum's zone is not finished

`DNSZoneNotReady` means a zone exists here but is not ready to be used. That is
a DNS problem rather than a load balancer one. Name the boundary and hand off to
the DNS tools with the zone's name.

Do not try to diagnose a zone from the load balancer. If the project is not
entitled to Datum's DNS at all, say so — the honest answer is that Datum is not
their DNS provider, and give them the record to create with whoever is.
