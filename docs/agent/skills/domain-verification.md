# Skill: proving you own a domain

Use when a hostname's `ownership-verified` step is not `ok`, or the cause is a
`Verified` reason on a domain.

## What has to be true

Datum will not point a hostname anywhere until the customer has proven they
control its domain. The proof is a DNS record with an exact name, type and
value, which Datum generates and which must be publicly visible.

`alb_diagnose` returns that record on the hostname's `domain`, as
`verificationRecord`. Give all three fields verbatim. Do not paraphrase them,
do not reformat them, and do not add quotes.

> Create this DNS record with your DNS provider:
>
> - Name: `_datum-challenge.example.com`
> - Type: `TXT`
> - Value: `token-abc`

## Reading the reason

| Reason | What happened | What to say |
|---|---|---|
| `PendingVerification` | Datum is waiting for the record | The record has not been seen yet. This waits on them, not on Datum |
| `RecordNotFound` | Datum looked and found nothing | Check the exact name, and that it is publicly visible |
| `VerificationRecordContentMismatch` | The record is there, the value is wrong | Replace the value exactly. Remove any older record from a previous attempt |
| `UnexpectedResponse` | Something answered, but not as expected | Check the domain resolves at all and nothing is answering in front of it |
| `InternalError` | Datum's own fault | Say so, say to raise it, and do not suggest changes to their DNS |
| `InvalidApex` | Not a registrable domain | They gave a public suffix like `com` or `co.uk`. Use the domain they registered |

## Why "not found" is usually not a typo

Before suggesting the value is wrong, check the three things that look like a
missing record and are not:

- **The record is in a split-horizon or internal zone.** Datum queries it the way
  the public internet does. A record only their office can see does not count.
- **It has not spread yet.** A record created a minute ago may not be visible
  from where Datum looks. This is a real wait.
- **The name was expanded twice.** Many DNS interfaces append the zone name
  automatically, turning `_datum-challenge.example.com` into
  `_datum-challenge.example.com.example.com`. Ask them to check the record's
  full name as their provider displays it. This is the single most common cause
  of `RecordNotFound` on a record the customer swears they created.

## Do not give this a deadline

Verification waits on a person creating a record. It is classified as theirs to
act on, not as something in flight, and it carries no expected duration on
purpose. Telling a customer "this usually completes in ten minutes" is a claim
about how fast they are, phrased as a promise about Datum.

Say what Datum is waiting for and that it picks the record up on its own once it
is visible.

## After it verifies

Ownership is one step. The hostname still needs its DNS record and its
certificate, and each takes its own time. Do not say the hostname is working
until its steps say so and a request succeeds.
