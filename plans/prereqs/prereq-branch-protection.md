# Prerequisite: Update Branch Protection Rules

## Why

Phase 1 introduces real tests with coverage reporting. Adding a coverage threshold to branch protection ensures no PR can merge with insufficient test coverage. Currently, only the "test" status check is required.

## Steps

### 1. Review current branch protection

```bash
gh api repos/atlasshare/atlax/branches/main/protection | jq '.required_status_checks'
```

This shows the current required status checks (should include "test").

### 2. Add Codecov status check (optional but recommended)

If you want to enforce coverage thresholds via Codecov:

**Option A: Codecov config file**

Create `codecov.yml` in the project root:

```yaml
coverage:
  status:
    project:
      default:
        target: 90%
        threshold: 2%
    patch:
      default:
        target: 90%
```

```bash
cd ~/projects/atlax
# Create the config
cat > codecov.yml << 'EOF'
coverage:
  status:
    project:
      default:
        target: 90%
        threshold: 2%
    patch:
      default:
        target: 90%
EOF

git checkout -b chore/codecov-config
git add codecov.yml
git commit -m "chore: add Codecov configuration with 90% coverage target"
git push -u origin chore/codecov-config
```

**Option B: Add Codecov status checks to branch protection**

After the Codecov config is merged, go to:
1. GitHub repo -> Settings -> Branches -> main -> Edit
2. Under "Require status checks to pass before merging"
3. Search for and add: `codecov/project` and `codecov/patch`
4. Save changes

Or via CLI:

```bash
# Note: updating branch protection via API replaces the entire config.
# Be careful to include existing checks.
gh api repos/atlasshare/atlax/branches/main/protection \
  -X PUT \
  -f required_status_checks='{"strict":true,"contexts":["test","codecov/project"]}' \
  -f enforce_admins=false \
  -f required_pull_request_reviews='{"required_approving_review_count":1}'
```

### 3. Verify the update

```bash
gh api repos/atlasshare/atlax/branches/main/protection | jq '.required_status_checks.contexts'
# Should include "test" and optionally "codecov/project"
```

## Done When

- Branch protection includes "test" status check (already in place)
- Codecov config (`codecov.yml`) merged with 90% target (if using Codecov checks)
- Optionally: "codecov/project" added to required status checks
