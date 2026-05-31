# CLA gate — setup & operation

How the Contributor License Agreement is enforced on pull requests, and the
one-time setup a maintainer must do.

## How it works

- The CLA text lives in [`CLA.md`](../../CLA.md).
- The gate is a GitHub Action (`.github/workflows/cla.yml`) using
  [`contributor-assistant/github-action`](https://github.com/contributor-assistant/github-action),
  pinned to v2.6.1 by commit SHA. **No third-party app** is installed and no
  external service receives access to the org.
- When a contributor opens a PR, the action posts a comment asking them to sign.
  The contributor signs by posting a comment:
  `I have read the CLA Document and I hereby sign the CLA`
- Signatures are recorded **in this repo**, on a dedicated `cla-signatures`
  branch, in `signatures/version1/cla.json`. Each signature stores the GitHub
  username, a timestamp, and the PR number — a durable, auditable record.
- A contributor signs **once**; all their future PRs pass automatically.
- Bots and elicify.ai maintainers are allow-listed (see `allowlist:` in the
  workflow) so internal/automated PRs are not gated.

## One-time setup (maintainer)

### Step 1 — Seed the signatures branch (one-time)

Signatures live on a dedicated `cla-signatures` branch in this same repo. Create it
once with an empty ledger so the action only ever has to *update* the file:

```bash
git switch --orphan cla-signatures
git rm -rf . >/dev/null 2>&1 || true
mkdir -p signatures/version1
printf '{"signedContributors":[]}' > signatures/version1/cla.json
git add signatures/version1/cla.json
git commit -m "chore(cla): initialize empty CLA signatures ledger"
git push -u origin cla-signatures
git switch -   # back to your previous branch
```

**No Personal Access Token or repository secret is required.** The workflow grants its
built-in `GITHUB_TOKEN` `contents: write`, which commits to this branch directly — and
it works for fork PRs too, because `pull_request_target` runs in the base repo's
context. (Avoiding a fine-grained org PAT also removes an expiry/approval failure mode:
on an org-owned repo an unapproved PAT silently lacks write and the action fails with
"Resource not accessible by integration".)

### Step 2 — Let the check run once, then require it

1. Open (or wait for) any pull request. The CLA check will run and a status check
   named **`CLA Assistant`** / **`license/cla`** appears on the PR.
2. Add it to branch protection so unsigned PRs cannot merge:
   - UI: **https://github.com/elicify-ai/omnipus/settings/branches** → edit the
     `main` rule → *Require status checks to pass before merging* → add the CLA
     check → Save.
   - Or via CLI (a maintainer can run this once the check has appeared):
     ```bash
     gh api -X PATCH repos/elicify-ai/omnipus/branches/main/protection/required_status_checks \
       -f 'contexts[]=CLA Assistant'
     ```

## Updating the CLA later

If the CLA text changes materially, bump the signature version so contributors
re-sign the new terms:

1. Edit `CLA.md`.
2. In `.github/workflows/cla.yml`, change `path-to-signatures` to
   `signatures/version2/cla.json`.
3. Existing signatures against version1 remain on record; contributors are asked
   to sign version2 on their next PR.

## Notes

- The `cla-signatures` branch holds the signature record (seeded in Step 1); do not
  delete it.
- The workflow uses `pull_request_target`, which runs in the context of the base
  repo so the token and secrets are available even for fork PRs. The action only
  reads PR metadata and writes the signatures file; it does not run contributor
  code.
