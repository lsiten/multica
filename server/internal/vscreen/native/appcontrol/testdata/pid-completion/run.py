import pathlib,subprocess,json,sys,hashlib
fixture=pathlib.Path(__file__).resolve().parent;source=fixture.parent.parent;out=pathlib.Path(sys.argv[1]).resolve();out.mkdir(parents=True,exist_ok=True)
cmd=['xcrun','clang','-fobjc-arc','-fblocks','-framework','AppKit','-framework','ApplicationServices','-framework','ScreenCaptureKit','-I',str(source),'-I',str(fixture),'-include',str(fixture/'overrides.h'),str(source/'input_darwin.m')]
if (source/'completion_darwin.m').exists():cmd.append(str(source/'completion_darwin.m'))
cmd += [str(fixture/'probe.m'),'-o',str(out/'probe')]
build=subprocess.run(cmd,capture_output=True,text=True);run=subprocess.run([str(out/'probe')],capture_output=True,text=True) if build.returncode==0 else build
(out/'result.json').write_text(json.dumps({'compile':cmd,'build_exit':build.returncode,'build_output':build.stdout+build.stderr,'run_exit':run.returncode,'stdout':run.stdout,'stderr':run.stderr},indent=2))
(out/'source-hashes.json').write_text(json.dumps({str(p):hashlib.sha256(p.read_bytes()).hexdigest() for p in [source/'input_darwin.m',source/'native_internal.h',fixture/'probe.m',fixture/'overrides.h',fixture/'run.py'] + ([source/'completion_darwin.m'] if (source/'completion_darwin.m').exists() else [])},indent=2))
print('build',build.returncode,'run',run.returncode,run.stdout)
sys.exit(int(build.returncode!=0 or run.returncode!=0))
