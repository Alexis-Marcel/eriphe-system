# Eriphe on Unraid

`eriphe.xml` is the Unraid template of `ghcr.io/alexis-marcel/eriphe`, Eriphe's launcher: one
container that creates and keeps running Eriphe's own containers (eriphe-core, eriphe-node, the
engine that alone holds the Docker socket), applies the updates started from the workspace and then updates itself. How it
works, and the same install on any Linux with Docker: [install/README.md, One
container](../install/README.md#one-container-nas-any-linux-with-docker).

## Install it from the template, before it is in Community Applications

1. Docker → **Add Container** → Template repositories (at the bottom of the page): add
   `https://github.com/Alexis-Marcel/eriphe-system` and save. Or copy `eriphe.xml` to
   `/boot/config/plugins/dockerMan/templates-user/my-eriphe.xml` on the flash drive.
2. Docker → **Add Container** → Template: **eriphe**. Check the data folder
   (`/mnt/user/appdata/eriphe`) and the ports (8443, 8800), then **Apply**.
3. Its log (the container's icon → Logs) ends with the workspace's address and a setup code:
   open the WebUI, `/setup`, and give the code.
4. Share the folders Eriphe may work with, from the NAS's terminal (the `>_` icon at the top):
   `docker exec eriphe eriphe share add "/mnt/user/1 PROJECTS"` (read only; `--drop` lets it
   write its results in its results subfolder, `--manage` lets it change the folder, `--copyable`
   allows copies on your other machines). `docker exec eriphe eriphe share list` shows them,
   `remove` stops one. Eriphe's tasks get each folder shared alone; nothing else of the NAS, not
   `/mnt/user` nor `appdata`, is mounted into eriphe-node or its tasks. Nothing restarts: the next
   task has the folder (a task at work keeps the folders it was given until it ends); only the
   copies to your other machines pause a few seconds when the folders that may be copied change.

The template runs the launcher hardened (Extra Parameters: `--cap-drop=ALL --cap-add=CHOWN
--cap-add=DAC_OVERRIDE --cap-add=FOWNER --read-only --tmpfs /tmp --security-opt
no-new-privileges`): it keeps only what it needs to write eriphe-core's folder. A container made
from an earlier template keeps its own settings: add them by hand (Docker → eriphe → Edit →
Advanced View → Extra Parameters).

The containers it makes show on the Docker page as containers without a template (eriphe-core,
eriphe-node, eriphe-engine, eriphe-syncthing): leave them to it, it recreates them on updates. To remove
everything: `docker exec eriphe eriphe-launcher uninstall` (with `--data` to delete
`/mnt/user/appdata/eriphe` too), then remove the `eriphe` container.

## Publishing it in Community Applications

Community Applications (CA) lists templates from public GitHub repositories that its maintainers
have accepted. Nothing here is submitted: the steps are for the repository's owner.

What CA requires (https://ca.unraid.net/submit/help):

- a **public, active GitHub repository** with an OSI-approved `LICENSE` at its root (AGPL-3.0 is
  one);
- valid Docker template XML, **one file per app** (`eriphe.xml`), each with a `<Repository>`, and
  readable `<Name>` and `<Overview>` for the moderators; `<Support>`, `<Project>`, `<Icon>` and
  `<TemplateURL>` are recommended (`eriphe.xml` has them all);
- a **`ca_profile.xml` at the repository's root**, named exactly so, with a non-empty
  `<Profile>` (`<Icon>` and `<WebPage>` recommended; `<Forum>`, `<Discord>`, `<DonateLink>`
  optional). `ca_profile.xml` here is ready, but in `unraid/`.

A support thread on the Unraid forums is not required (`<Forum>` is optional): `<Support>` points
to the GitHub issues. One can be opened later in the forums' Docker Containers section, and put
in `<Support>` and in `ca_profile.xml`'s `<Forum>`.

Steps:

1. **The repository CA scans.** Either a small one of its own, from CA's starter
   (https://github.com/unraid/unraid-community-apps-starter, "Use this template"), e.g.
   `Alexis-Marcel/unraid-templates`: `ca_profile.xml` at its root, `eriphe.xml` in
   `templates/`, with `<TemplateURL>` changed to that file's raw URL, and a `LICENSE`. That keeps
   this repository's other XML out of CA's scan. Or this repository itself, with
   `ca_profile.xml` moved to its root.
2. Check the image is public: https://github.com/Alexis-Marcel?tab=packages, package `eriphe`,
   Package settings → visibility **Public** (a first push from CI may make it private), and that
   `ghcr.io/alexis-marcel/eriphe:latest` exists (the first release after this change publishes
   it).
3. Try the template on the NAS from that repository (above, "Install it from the template").
4. https://ca.unraid.net/submit → **Start Submission** (https://ca.unraid.net/submit/new),
   give the repository ("Your Apps Repository"), run **Validate**, fix what it says, then
   **Scan** (it reads the templates and `ca_profile.xml`, and looks for duplicates in CA). Run
   both again after each change of the XML. A walkthrough: https://www.youtube.com/watch?v=j11y8ZnfXhw.
5. Submit. Approved apps appear in CA after its next catalog build.

Later changes need no new submission: CA reads the templates again from the repository.
