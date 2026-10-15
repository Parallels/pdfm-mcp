# Demo: QA environment from a base VM

One prompt that clones a base Ubuntu VM, adds CPU, provisions Node, runs the
tests of `Parallels/prl-devops-ui`, starts the dev server, shows it on the VM
screen, and rolls the VM back. Uses only the clone; the base VM is never
touched.

## Before you record (once)

Base VM (Ubuntu Desktop, ARM or x64):

- [ ] Parallels Tools installed; `prlctl exec <vm> whoami` works.
- [ ] Auto-login enabled, **Xorg session** (not Wayland: login screen gear icon, or
      `WaylandEnable=false` in `/etc/gdm3/custom.conf`). Needed so a browser can open on `DISPLAY=:0`.
- [ ] A browser installed (`firefox` or `chromium`), screen lock and blank-screen disabled.
- [ ] `curl`, `git`, `ca-certificates` present. Passwordless sudo is not needed: installs use
      the server's `elevated` exec (root; unverified on Linux guests, check it in the dry run).
- [ ] Disk: the Rust build output (`src-tauri/target`) needs roughly 5 to 10 GB; give the base VM headroom. RAM 8 GB or more.
- [ ] Optional speed-up (recommended): pre-install Node 24, rustup (stable), and the Tauri apt packages
      from the project README. The prompt installs only what is missing. The first Rust compile is
      still the long part; more cores shorten it, which is the visible payoff of the core increase.
- [ ] If the Tauri window is blank in the VM, the prompt already sets `WEBKIT_DISABLE_DMABUF_RENDERER=1`;
      also try `WEBKIT_DISABLE_COMPOSITING_MODE=1` if needed.
- [ ] Shut the base VM down. Delete leftover clones from earlier runs.
- [ ] Server running in fleet mode, no `--read-only`, no `--allow-vms` blocking the new clone name
      (or allow `devops-ui-qa-*`).

Edit the two values at the top of the prompt: `BASE_VM` and the clone name.

## The prompt

```text
You are running a live QA demo with the Parallels Desktop MCP tools. Work only on a new clone; never modify, start or delete the base VM. Keep your narration to one short line per step. Use parallels_vm_exec_start + parallels_vm_exec_result (polling) for anything that can take more than 30 seconds, and parallels_vm_sequence where it saves round trips (waits and retries). Never print credentials. The commands run without a shell, so wrap pipes, &&, redirects and variables in `sh -c "..."`. Guest exec starts with a bare environment: in every sh -c command that uses node, npm, cargo or make, begin with `export PATH=$HOME/.cargo/bin:/usr/local/bin:$PATH;`.

BASE_VM = "linux_dev_machine_base"
CLONE   = "devops-ui-qa-<today's date and HH-MM>"
REPO    = https://github.com/Parallels/prl-devops-ui

1. PREPARE. List the VMs. Confirm BASE_VM exists and is stopped, and that CLONE does not exist. Stop and tell me if not.

2. CLONE BASE_VM to CLONE (full clone). Confirm it appears stopped.

3. MORE CPU. Read CLONE's current cpus and memsize_mb with parallels_vm_info. With the VM stopped, use parallels_vm_set to double cpus (at least +2, at most 8) and raise memory to at least 8192 MB. Verify with parallels_vm_info and show a before/after line.

4. START CLONE and wait until the guest answers `echo ready` via exec (retry every 5 s, up to 3 minutes). Run `nproc` and `free -m` in the guest to prove the new resources.

5. CHECKPOINT. Create a snapshot named "clean-baseline" with a one-line description.

6. PROVISION. In the guest check `git --version`, `node --version`, `cargo --version` and the Tauri system libraries (`dpkg -s libwebkit2gtk-4.1-dev libgtk-3-dev`). Install only what is missing, with elevated async exec and polling: Node 24 via NodeSource; the apt packages from the project README (libwebkit2gtk-4.1-dev libgtk-3-dev libayatana-appindicator3-dev librsvg2-dev patchelf libssl-dev pkg-config gcc g++); Rust stable via rustup (as the normal user, minimal profile). Print the final versions.

7. FETCH AND INSTALL. As the normal guest user: git clone REPO into ~/prl-devops-ui (if the folder exists, stop). Then create .env: copy .env.example if it exists, otherwise write exactly these placeholder demo values: VITE_DEVOPS_API_URL=http://localhost:5680, VITE_DEVOPS_USERNAME=admin, VITE_DEVOPS_PASSWORD=changeme, VITE_DEVOPS_EMAIL=admin@example.com. Then run `make install` (async, poll, one-line status every minute). Run `make check` and show whether the Tauri environment is OK; if not, fix what it names and re-check once.

8. TEST GATE. Run `npm run test -- --run` (non-watch) async. Report passed/failed/total test counts and wall-clock time. If any test fails: do NOT start the app, show the failing test names and the first error lines, summarize, and stop (leave the VM running).

9. RUN THE DESKTOP APP. Only if all tests passed: start the Tauri app on the guest desktop as an async job, as the normal user, with the display set: sh -c "export PATH=$HOME/.cargo/bin:/usr/local/bin:$PATH DISPLAY=:0 XAUTHORITY=/run/user/$(id -u)/gdm/Xauthority WEBKIT_DISABLE_DMABUF_RENDERER=1; cd $HOME/prl-devops-ui && make dev". Exec starts with a bare environment (no login profile, no desktop session variables), which is why PATH, DISPLAY and XAUTHORITY are set explicitly; if the window does not appear, check that /run/user/<uid>/gdm/Xauthority exists and report what you found. The first Rust build is slow (often 10 to 25 minutes): poll the job output every 60 s, report the compile progress in one line, and stop waiting after 40 minutes. The app is up when the output shows the build finished and the app running, and `pgrep -f prl-devops-ui` finds a process.

10. SHOW IT. Wait for the screen to settle (parallels_vm_sequence wait until settled), then use parallels_vm_observe to confirm the app window shows real text (report two or three words you read). If the window is blank or white, retry once after 10 s and say so. Take one screenshot with parallels_vm_capture. If the app shows a login or "cannot reach API" screen, say so plainly: it needs a Parallels DevOps backend on port 5680, which this demo does not run. Do not claim anything you could not see.

11. ROLL BACK. Switch CLONE to the snapshot "clean-baseline" (revert), then verify with exec that ~/prl-devops-ui no longer exists. This shows the machine is disposable.

12. SUMMARY. A short table: VM name, cores and RAM before/after, Node version, npm install time, test result (passed/failed/total, seconds), app reachable (yes/no), what the screenshot showed, rollback verified (yes/no). Then two sentences on anything that went wrong or was skipped. Leave CLONE in place; do not delete it.
```

## Notes for the runs

- Dry-run steps 9 and 10 first on a hand-prepared VM; the browser-on-screen step is the most
  likely to need base-VM tweaks (Xorg, browser choice, `DISPLAY`).
- A cold run is dominated by the first Tauri/Rust compile. For the video, pre-bake the toolchain
  (see checklist) and cut or speed up the waiting. For consistent timings, run the first compile once
  on the base VM's clone, then keep `src-tauri/target` out of the base.
- The `observe` step runs OCR locally, but its text output reaches the model provider. Keep
  the VM screen free of personal data.
- Core-count comparison (run the tests at 2 vs 6 cores) is a good extra but needs a stop, `parallels_vm_set`
  and start between runs; add it as a second prompt rather than lengthening this one.
