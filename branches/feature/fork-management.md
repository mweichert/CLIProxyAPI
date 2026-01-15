# Feature: Fork Management

## Summary

Adds declarative fork management infrastructure for maintaining this fork of CLIProxyAPI, including automated branch composition, Docker image builds, and documentation.

## Components

### Fork Configuration (`fork.yaml`)

Declarative YAML file defining:
- Upstream repository and branch
- Local feature/bugfix branches to compose
- Branch dependencies and documentation links

### Build Script (`scripts/build-fork.py`)

Python script that automates:
1. **Sync** - Fetch upstream and reset `main` to match
2. **Rebase** - Rebase all feature branches onto their base (topologically sorted)
3. **Build** - Create `fork` branch by merging all branches
4. **Tag** - Auto-tag if fork changed (e.g., `1.2.3-fork.1`)

Usage:
```bash
python3 scripts/build-fork.py           # Full rebuild
python3 scripts/build-fork.py --dry-run # Preview changes
```

### GHCR Workflow (`.github/workflows/build-ghcr.yml`)

GitHub Actions workflow that:
- Triggers on push to `fork` branch or tags
- Builds Docker image using existing `Dockerfile`
- Pushes to `ghcr.io/mweichert/cliproxyapi`

### Branch Documentation (`branches/`)

Structured documentation for each fork-specific branch:
- `branches/feat/` - Feature branch docs
- `branches/fix/` - Bugfix branch docs
- `branches/feature/` - Infrastructure branch docs

## Branch Structure

| Branch | Purpose |
|--------|---------|
| `main` | Mirror of upstream `router-for-me/CLIProxyAPI:main` |
| `fork` | Composed working branch (auto-built) |
| `feat/*` | Custom feature branches |
| `fix/*` | Bug fix branches |
| `feature/*` | Infrastructure branches |

## Workflow

### Adding a New Branch

1. Create branch from `main`:
   ```bash
   git checkout -b feat/my-feature main
   ```

2. Make changes, commit, push

3. Create documentation in `branches/feat/my-feature.md`

4. Add to `fork.yaml`:
   ```yaml
   - name: feat/my-feature
     base: main
     description: My feature description
     docs: branches/feat/my-feature.md
   ```

5. Run build script:
   ```bash
   python3 scripts/build-fork.py
   ```

### Syncing with Upstream

The build script handles upstream sync automatically:
```bash
python3 scripts/build-fork.py
```

This fetches upstream changes, rebases all branches, and rebuilds `fork`.

## Dependencies

- Python 3.10+
- PyYAML (`pip install pyyaml`)
- Git with upstream remote configured

## Files Added

| File | Purpose |
|------|---------|
| `fork.yaml` | Fork composition configuration |
| `scripts/build-fork.py` | Build orchestration script |
| `.github/workflows/build-ghcr.yml` | Docker build/push workflow |
| `branches/feature/fork-management.md` | This documentation |
| `branches/feat/traffic-debug-mode.md` | Traffic debug feature docs |
| `branches/fix/anthropic-models-endpoint-format.md` | Anthropic fix docs |
