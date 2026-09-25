# Skill: a certificate that has not been issued

Use when a hostname's `certificate` step is not `ok`, or HTTPS fails on one
hostname while HTTP works.

## Check DNS first, every time

A certificate cannot be issued until the hostname resolves publicly to this load
balancer. The authority checks that before issuing.

So **a certificate that is not issued is usually a symptom, and the cause is one
step earlier.** Look at the same hostname's `dns-record` and
`ownership-verified` steps before you say anything about certificates. If either
is not `ok`, that is the answer; fix it and the certificate follows on its own.

Only when DNS is genuinely in place is the certificate itself the story.

## Reading the reason

| Reason | What it means |
|---|---|
| `Pending` | Not issued yet. Normal shortly after a hostname is attached |
| `ChallengeInProgress` | Being issued right now — the authority is checking the name points here |
| `ProvisioningFailed` | An attempt failed |
| `CertificateIssued` | Done; HTTPS works |

`ChallengeInProgress` is the encouraging one: the check is already running, which
means the name resolved. It is gated on public DNS caches, so it is genuinely
slower than it looks.

## When it has been too long

Certificates carry an expected window of about thirty minutes. Past that,
`alb_diagnose` reports the cause as `stalled` rather than transient. Do not keep
telling someone to wait once it has stalled — say it has outlived what is normal
and that it is worth raising, and give them the hostname and the reason.

## `ProvisioningFailed` with DNS that looks fine

Check, in this order:

1. **Does the hostname resolve to this load balancer from the public internet?**
   Not from their office, not from a cache — publicly. A CNAME pointing at the
   wrong target, or at an old load balancer, fails exactly this way.
2. **Is there a CAA record on the domain?** A CAA record that does not permit
   the authority Datum uses blocks issuance for the whole domain, and the
   customer often does not know it is there.
3. **Is the name still claimed by this load balancer?** If the `claimed` step
   also moved, the hostname changed hands and that is a consequence of it, not a separate problem.

If all three are fine, it is Datum's. Say so rather than sending them round
again.

## What not to say

Do not explain the issuance protocol, or name the component that requests
certificates. The customer wants to know whether HTTPS works on their hostname,
why not, and what they can do. "The certificate for `app.example.com` has not
been issued yet, because the hostname does not resolve to this load balancer
yet" is the whole answer.
