# Skill: a hostname that does not reach the load balancer

Use when one hostname does not work and others do, or when a newly attached
hostname has never worked.

## The four steps, in order

`alb_get` and `alb_diagnose` both return each hostname's progress. Each step
gates the next, so **work on the first one that is not `ok`** and ignore the
rest until it is.

| Step | What it means | If it is not ok |
|---|---|---|
| `claimed` | This load balancer holds the hostname | Something else on Datum has it → below |
| `ownership-verified` | You proved you own the domain | → `domain-verification` |
| `dns-record` | A DNS record points the hostname here | → below, then `dns-delegation` |
| `certificate` | HTTPS works on it | → `certificate-not-issued` |

## States that are not failures

- **`notApplicable`** on the DNS record step means Datum does not run this
  domain's DNS. That is normal, not a fault. The customer creates a CNAME from
  their hostname to the generated hostname with their own provider. Nothing is
  pending and nothing is coming.
- **`notReported`** means nothing published anything about that step. It is
  neither pass nor fail. Two are expected today: the generated hostname has no
  DNS-record status at all, and ownership is never reported on the hostname
  itself — it comes from the domain, and `notReported` there means no domain
  covering the hostname was found.

Never tell a customer to wait for a `notApplicable` or `notReported` step.

## The generated hostname

Every load balancer gets one, of the form `<id>.datumproxy.net`. It needs no
claim, no ownership proof and no record of the customer's. It is what they point
a CNAME at.

It publishes no DNS-record or certificate status. Do not read that as a problem
and do not wait on it. To check it works, make a request.

## A hostname that is already claimed

`claimed` = `failed` with reason `InUse` means the hostname is held elsewhere on
Datum. **Who holds it is not visible** — it may be in another customer's
project. Do not send them looking for it.

Their options are: use a different hostname, release it from wherever they
control it, or, if they believe it should be theirs, raise it with Datum.

## A DNS record that will not be written

Read the reason on the `dns-record` step:

| Reason | What it means |
|---|---|
| `DomainNotVerified` | Ownership first → `domain-verification` |
| `DNSAuthorityMissing` | Datum does not answer for this domain → `dns-delegation` |
| `DNSZoneNotReady` | Datum's zone for it is unfinished → `dns-delegation` |
| `ConflictWithUserRecord` | A record already exists that Datum did not write. Remove it, or use a different hostname |
| `RecordCreationFailed` | Datum's. Say so and say to raise it |
| `Pending` / `RetryPending` | In flight. Check how long — past 15 minutes it is stuck |

## Checking your work

Once the steps read `ok`, confirm end to end rather than declaring victory from
status:

```
curl -sSI https://<hostname>
```

Public DNS caches mean a change can be right and still not visible for a while.
That is a real wait and worth saying — but say it as a property of DNS, not as a
promise about Datum.
