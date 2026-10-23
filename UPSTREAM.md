# Upstream provenance

ShellBeam Server is an independently maintained fork, not an official Komari distribution.

| Field | Value |
| --- | --- |
| Fork | https://github.com/0xJean/shellbeam-server |
| Upstream | https://github.com/komari-monitor/komari |
| Baseline tag | `1.6.0` |
| Baseline commit | `1b76b7ad5bc3de6529bb7f0577069b4ac241fec3` |
| API compatibility version | `1.6.0` |
| ShellBeam development version | `0.1.0-dev` |
| License | MIT; original copyright retained in `LICENSE` |

This file is the source of truth for the upstream baseline. When updating the baseline, change the tag/commit together and verify API, Agent, plugin, and storage compatibility before releasing.

The Go module path remains `github.com/komari-monitor/komari` to keep upstream imports and updates straightforward. Keeping that path is an implementation choice, not a claim of official affiliation.

ShellBeam changes replace product-facing identity and the bundled frontend, add separate product version metadata, and supply independent build/release/container configuration. Komari API names, JSON fields, Agent endpoints, old environment variables, and database names are deliberately retained for compatibility.

The official `komari-web` frontend and upstream installation script are not part of this distribution. The minimal service entry page is authored for this fork. Upstream README files remain available in the original Git history and at the pinned source commit; see [upstream maintenance](docs/upstream.md).

Redistribution must include `LICENSE`, `NOTICE`, and the relevant third-party license texts. Build/release tooling collects dependency license and attribution files from the pinned Go module graph into `THIRD_PARTY_NOTICES/` without changing the original legal texts.
