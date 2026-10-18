# Contributing

Thanks for your interest in ncoda.

## Developer Certificate of Origin (DCO)

This project uses the DCO (https://developercertificate.org/). Every commit
must be signed off:

```bash
git commit -s
```

By signing off you certify that you have the right to submit the contribution
under the project's license (Apache-2.0 for this repository).

## Before you send a change

```bash
make check   # build / vet / gofmt / tests
make push    # uncached tests + CLI contract smoke
```

- Keep language semantics unchanged unless the change goes through the
  language-change review process.
- One logical change per commit; keep diffs reviewable.
- New behavior needs a test that fails without the change.

## Licensing of contributions

Contributions to this repository are licensed under Apache-2.0. No CLA is
required; the DCO sign-off is sufficient.
