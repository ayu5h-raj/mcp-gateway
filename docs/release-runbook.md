# Release Runbook

## Cutting a release

```sh
# 1. Update CHANGELOG.md with the new version section.
# 2. Tag and push.
git tag -a v1.x.y -m "v1.x.y"
git push origin v1.x.y
# 3. Watch the release workflow.
gh run watch  # or check the Actions tab
```

The release workflow (`.github/workflows/release.yaml`) does:
- Builds darwin arm64+amd64 + linux arm64+amd64 binaries.
- Creates a GitHub Release with archives, checksums, and `install.sh`.
- Pushes an updated `Formula/mcp-gateway.rb` to `ayu5h-raj/homebrew-tap`.

## Verifying after release

```sh
brew untap ayu5h-raj/tap 2>/dev/null
brew install ayu5h-raj/tap/mcp-gateway
mcp-gateway --version    # matches the tag
mcp-gateway init -y      # smoke
```

Then:

```sh
brew uninstall mcp-gateway
curl -fsSL https://raw.githubusercontent.com/ayu5h-raj/mcp-gateway/main/scripts/install.sh | sh
mcp-gateway --version
```

## Rotating HOMEBREW_TAP_TOKEN

The `HOMEBREW_TAP_TOKEN` is a fine-grained GitHub PAT with `contents:write` on the `homebrew-tap` repo only. It expires; rotate annually or on any suspicion of compromise.

1. Go to https://github.com/settings/personal-access-tokens
2. Create a new fine-grained token. Scope: only `ayu5h-raj/homebrew-tap`. Permission: `Contents: read & write`. Expiry: 1 year.
3. Copy the token.
4. In `ayu5h-raj/mcp-gateway` → Settings → Secrets and variables → Actions → update `HOMEBREW_TAP_TOKEN`.
5. Revoke the old token.

## Hotfix release

For a fix that needs to ship out-of-cycle:

```sh
git checkout main
git pull
# fix
git commit -am "fix: ..."
git push
git tag -a v1.x.(y+1) -m "v1.x.(y+1)"
git push origin v1.x.(y+1)
```

Same workflow runs.
