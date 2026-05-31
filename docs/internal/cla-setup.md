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

### Step 1 — Create a fine-grained Personal Access Token (scoped to this repo only)

1. Go to **https://github.com/settings/personal-access-tokens/new**
2. **Token name:** `omnipus-cla-signatures`
3. **Resource owner:** `elicify-ai`
4. **Expiration:** 1 year (set a reminder to rotate).
5. **Repository access:** *Only select repositories* → choose **`elicify-ai/omnipus`**.
6. **Permissions → Repository permissions → Contents:** **Read and write**.
   (Leave everything else as "No access".)
7. Click **Generate token** and copy it.

This token can only write contents to the one repo — nothing else, no other repos,
no other orgs.

### Step 2 — Add it as a repository secret

1. Go to **https://github.com/elicify-ai/omnipus/settings/secrets/actions/new**
2. **Name:** `PERSONAL_ACCESS_TOKEN`
3. **Secret:** paste the token from Step 1.
4. **Add secret.**

### Step 3 — Let the check run once, then require it

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

- The `cla-signatures` branch is created automatically on first run; do not delete
  it — it is the signature record.
- The workflow uses `pull_request_target`, which runs in the context of the base
  repo so the token and secrets are available even for fork PRs. The action only
  reads PR metadata and writes the signatures file; it does not run contributor
  code.
