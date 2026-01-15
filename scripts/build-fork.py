#!/usr/bin/env python3
"""
Build the fork branch from fork.yaml configuration.

Usage:
    python3 scripts/build-fork.py           # Full rebuild: sync + rebase + build + push
    python3 scripts/build-fork.py --dry-run # Preview what would happen (no changes)
"""

import argparse
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

import yaml


@dataclass
class UpstreamConfig:
    remote: str
    branch: str


@dataclass
class BranchConfig:
    name: str
    base: str
    description: str


@dataclass
class ForkConfig:
    upstream: UpstreamConfig
    base: str
    branches: list[BranchConfig]


class ForkBuilder:
    def __init__(self, config: ForkConfig, repo_root: Path, dry_run: bool = False):
        self.config = config
        self.repo_root = repo_root
        self.dry_run = dry_run
        self.original_branch = self._get_current_branch()

    def _get_current_branch(self) -> str:
        """Get the current git branch name."""
        result = subprocess.run(
            ["git", "branch", "--show-current"],
            capture_output=True,
            text=True,
            check=True,
        )
        return result.stdout.strip()

    def _get_commit(self, ref: str) -> str | None:
        """Get the commit SHA for a ref, or None if it doesn't exist."""
        result = subprocess.run(
            ["git", "rev-parse", "--verify", ref],
            capture_output=True,
            text=True,
        )
        if result.returncode != 0:
            return None
        return result.stdout.strip()

    def _get_latest_upstream_tag(self) -> str:
        """Get the latest version tag from upstream.

        Looks for semver tags (e.g., v1.2.3 or 1.2.3) on the upstream branch.
        """
        # Fetch upstream tags
        subprocess.run(
            ["git", "fetch", self.config.upstream.remote, "--tags"],
            capture_output=True,
            check=True,
        )

        # Get tags merged into upstream/main
        result = subprocess.run(
            ["git", "tag", "--merged", f"{self.config.upstream.remote}/{self.config.upstream.branch}"],
            capture_output=True,
            text=True,
            check=True,
        )

        tags = result.stdout.strip().split("\n")
        tags = [t for t in tags if t]

        # Filter to semver-like tags (v1.2.3 or 1.2.3)
        semver_pattern = re.compile(r"^v?(\d+\.\d+\.\d+)$")
        semver_tags = []
        for tag in tags:
            match = semver_pattern.match(tag)
            if match:
                semver_tags.append((tag, tuple(map(int, match.group(1).split(".")))))

        if not semver_tags:
            return "0.0.0"

        # Sort by version tuple and return the highest
        semver_tags.sort(key=lambda x: x[1], reverse=True)
        return semver_tags[0][0].lstrip("v")

    def _get_next_fork_tag(self, version: str) -> str:
        """Determine the next fork tag number for the given version."""
        # List existing tags matching this version
        result = subprocess.run(
            ["git", "tag", "-l", f"{version}-fork.*"],
            capture_output=True,
            text=True,
            check=True,
        )

        existing_tags = result.stdout.strip().split("\n")
        existing_tags = [t for t in existing_tags if t]  # Filter empty strings

        if not existing_tags:
            return f"{version}-fork.1"

        # Find the highest fork number
        pattern = re.compile(rf"^{re.escape(version)}-fork\.(\d+)$")
        max_num = 0
        for tag in existing_tags:
            match = pattern.match(tag)
            if match:
                max_num = max(max_num, int(match.group(1)))

        return f"{version}-fork.{max_num + 1}"

    def git_run(self, *args: str, check: bool = True, capture: bool = False) -> subprocess.CompletedProcess:
        """Execute a git command with consistent error handling."""
        cmd = ["git", *args]
        cmd_str = " ".join(cmd)

        if self.dry_run:
            print(f"  [dry-run] {cmd_str}")
            return subprocess.CompletedProcess(cmd, 0, stdout="", stderr="")

        print(f"  $ {cmd_str}")
        return subprocess.run(
            cmd,
            check=check,
            capture_output=capture,
            text=True,
        )

    def sync_upstream(self) -> None:
        """Fetch upstream and reset base branch to match."""
        print(f"\n=== Syncing with upstream ({self.config.upstream.remote}/{self.config.upstream.branch}) ===")

        self.git_run("fetch", self.config.upstream.remote)
        self.git_run("checkout", self.config.base)
        self.git_run("reset", "--hard", f"{self.config.upstream.remote}/{self.config.upstream.branch}")
        self.git_run("push", "origin", self.config.base, "--force-with-lease")

    def _topological_sort(self) -> list[BranchConfig]:
        """Sort branches so dependencies are rebased before dependents."""
        # Build dependency graph
        branch_map = {b.name: b for b in self.config.branches}
        sorted_branches: list[BranchConfig] = []
        visited: set[str] = set()

        def visit(branch: BranchConfig) -> None:
            if branch.name in visited:
                return
            visited.add(branch.name)

            # If this branch depends on another branch (not base), visit that first
            if branch.base != self.config.base and branch.base in branch_map:
                visit(branch_map[branch.base])

            sorted_branches.append(branch)

        for branch in self.config.branches:
            visit(branch)

        return sorted_branches

    def rebase_branches(self) -> None:
        """Rebase all branches onto their base branches."""
        print("\n=== Rebasing branches ===")

        sorted_branches = self._topological_sort()

        for branch in sorted_branches:
            print(f"\nRebasing {branch.name} onto {branch.base}...")
            self.git_run("checkout", branch.name)
            self.git_run("rebase", branch.base)
            self.git_run("push", "origin", branch.name, "--force-with-lease")

    def build_fork(self) -> None:
        """Build the fork branch by merging all feature branches."""
        print("\n=== Building fork branch ===")

        # Start from base
        self.git_run("checkout", self.config.base)

        # Delete and recreate fork branch
        self.git_run("branch", "-D", "fork", check=False)
        self.git_run("checkout", "-b", "fork")

        # Merge each branch
        for branch in self.config.branches:
            print(f"\nMerging {branch.name}...")
            result = self.git_run("merge", "--no-edit", branch.name, check=False)
            if result.returncode != 0:
                print(f"\nError: Merge conflict while merging {branch.name}")
                print("Resolve conflicts, then run: git merge --continue")
                print(f"Or abort with: git merge --abort && git checkout {self.original_branch}")
                sys.exit(1)

        # Push fork branch
        self.git_run("push", "origin", "fork", "--force-with-lease")

    def tag_fork(self, old_fork_commit: str | None) -> str | None:
        """Tag the fork if it has changed. Returns the new tag name or None."""
        new_fork_commit = self._get_commit("fork")

        if old_fork_commit == new_fork_commit:
            print("\n=== No changes detected, skipping tag ===")
            return None

        version = self._get_latest_upstream_tag()
        tag_name = self._get_next_fork_tag(version)

        print(f"\n=== Tagging fork as {tag_name} ===")
        self.git_run("tag", tag_name, "fork")
        self.git_run("push", "origin", tag_name)

        return tag_name

    def run(self) -> None:
        """Run the full rebuild: sync + rebase + build + tag."""
        if self.dry_run:
            print("=== DRY RUN MODE - No changes will be made ===")

        # Check for uncommitted changes
        result = subprocess.run(["git", "diff-index", "--quiet", "HEAD", "--"], capture_output=True)
        if result.returncode != 0:
            print("Error: You have uncommitted changes. Please commit or stash them first.")
            sys.exit(1)

        # Save current fork commit to detect changes
        old_fork_commit = self._get_commit("fork")

        self.sync_upstream()
        self.rebase_branches()
        self.build_fork()

        # Tag if changed
        tag_name = self.tag_fork(old_fork_commit)

        print("\n=== Done! ===")
        print(f"\nCommits in fork ahead of {self.config.base}:")
        subprocess.run(["git", "log", "--oneline", f"{self.config.base}..fork"])

        if tag_name:
            print(f"\nTagged as: {tag_name}")
            print(f"Docker image will be: ghcr.io/mweichert/cliproxyapi:{tag_name}")


def load_config(config_path: Path) -> ForkConfig:
    """Load and parse the YAML configuration file."""
    with open(config_path) as f:
        data = yaml.safe_load(f)

    upstream = UpstreamConfig(
        remote=data["upstream"]["remote"],
        branch=data["upstream"]["branch"],
    )

    branches = [
        BranchConfig(
            name=b["name"],
            base=b["base"],
            description=b.get("description", ""),
        )
        for b in data["branches"]
    ]

    return ForkConfig(
        upstream=upstream,
        base=data["base"],
        branches=branches,
    )


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Build fork branch from fork.yaml configuration",
        epilog="""
What this script does:
  1. Sync      Fetch upstream, reset 'main' to upstream/main, push main
  2. Rebase    Rebase each branch onto its base (topologically sorted)
  3. Build     Create 'fork' from main, merge all branches, push fork
  4. Tag       If fork changed, create and push tag (e.g., 1.2.3-fork.1)

All changes are pushed to origin automatically.

Configuration is read from fork.yaml in the repository root.
        """,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("--dry-run", action="store_true", help="Preview changes without executing (no git commands run)")
    args = parser.parse_args()

    # Find config file
    script_dir = Path(__file__).parent
    repo_root = script_dir.parent
    config_path = repo_root / "fork.yaml"

    if not config_path.exists():
        print(f"Error: {config_path} not found")
        sys.exit(1)

    config = load_config(config_path)
    builder = ForkBuilder(config, repo_root, dry_run=args.dry_run)
    builder.run()


if __name__ == "__main__":
    main()
