# Learnings

- No host Go executable is installed; use the pinned Go Docker image for checks.
- Emby Library/MediaFolders and Items endpoints expose the hierarchy; original ParentId must not be overwritten by the vloader library filter ID.
- Source download paths must be resolved through os.OpenRoot, not lexical prefix checks alone.
- GitHub connector is connected but exposes no repository-creation operation in this environment.
