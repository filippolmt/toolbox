# Toolbox container

`docker` here drives the host's daemon (Docker-outside-of-Docker): the daemon resolves every `docker run -v` / `--mount` source on the host, not in this container. Pass the workspace's host path, `$TOOLBOX_HOST_WORKSPACE` followed by the path below `/workspace`; a directory outside the workspace has no host counterpart. That covers the agent scratchpad, `/tmp` and `$HOME`: a mount of them fails or, worse, comes up empty with no error, and the tool inside then reports a misleading "file not found". Keep worktrees and fixtures you mount under the workspace.

Create parallel worktrees with Worktrunk, `wt switch -c <branch> --no-cd`, in place of Claude Code's own worktree isolation: the worktree lands beside the repository, where `wt list` shows it and `wt step prune` removes it once merged.
