import hashlib, json, pathlib, subprocess, sys
fixture = pathlib.Path(__file__).resolve().parent
source = fixture.parent.parent
out = pathlib.Path(sys.argv[1]).resolve()
out.mkdir(parents=True, exist_ok=True)
identity = (source / "identity_darwin.m").read_text()
(out / "guard-source.inc").write_text(identity[identity.index("NSString *ACGuard("):identity.index("NSDictionary *ACWindowValue")])
(out / "ended-source.inc").write_text(identity[identity.index("BOOL ACProcessEnded("):identity.index("id ACCopy(")])
failed = False
for name in ["session", "fence"]:
    cmd = ["xcrun", "clang", "-fobjc-arc", "-fblocks", "-framework", "AppKit", "-framework", "ApplicationServices", "-framework", "ScreenCaptureKit", "-I", str(source), "-I", str(out), "-include", str(fixture / ("native-probe-overrides.h" if name == "session" else "native-fence-overrides.h")), str(source / "bridge_darwin.m"), str(source / "completion_darwin.m")]
    if name == "fence": cmd.append(str(source / "input_darwin.m"))
    cmd += [str(fixture / ("native-" + name + "-probe.m")), "-o", str(out / name)]
    build = subprocess.run(cmd, capture_output=True)
    (out / (name + "-build.log")).write_bytes(build.stdout + build.stderr)
    run = subprocess.run([str(out / name)], capture_output=True) if build.returncode == 0 else build
    (out / (name + ".log")).write_bytes(run.stdout + run.stderr)
    (out / (name + ".exit.json")).write_text(json.dumps({"compile":cmd,"build_exit":build.returncode,"run":[str(out / name)],"exit":run.returncode}))
    print(name, "build", build.returncode, "run", run.returncode)
    failed |= run.returncode != 0
(out / "source-sha256.json").write_text(json.dumps({str(p):hashlib.sha256(p.read_bytes()).hexdigest() for p in list(source.glob("*.m"))+list(fixture.glob("*")) if p.is_file()}, indent=2))
sys.exit(int(failed))
