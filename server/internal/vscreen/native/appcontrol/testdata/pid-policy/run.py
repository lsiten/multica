import hashlib,json,pathlib,subprocess,sys
fixture=pathlib.Path(__file__).resolve().parent
source=fixture.parent.parent
out=pathlib.Path(sys.argv[1]).resolve();out.mkdir(parents=True,exist_ok=True)
identity=(source/'identity_darwin.m').read_text();inputs=(source/'input_darwin.m').read_text()
(out/'executable-source.inc').write_text(identity[identity.index('static NSDictionary *ACExecutableIdentity('):identity.index('NSDictionary *ACProcess(')])
(out/'press-key-source.inc').write_text(inputs[inputs.index('static NSString *pressKey('):inputs.index('static void releaseInterrupted(')])
(out/'release-source.inc').write_text(inputs[inputs.index('static void releaseInterrupted('):inputs.index('void ACMarkUncertain(')])
(out/'post-source.inc').write_text(inputs[inputs.index('static NSString *post('):inputs.index('static CGEventFlags flags(')])
cmd=['xcrun','clang','-fobjc-arc','-fblocks','-framework','AppKit','-framework','ApplicationServices','-framework','Security','-framework','Carbon','-framework','ScreenCaptureKit','-I',str(source),'-I',str(out),str(fixture/'probe.m'),'-o',str(out/'probe')]
build=subprocess.run(cmd,capture_output=True,text=True)
(out/'build.log').write_text(build.stdout+build.stderr)
run=subprocess.run([str(out/'probe')],capture_output=True,text=True) if build.returncode==0 else build
(out/'probe.json').write_text(run.stdout or json.dumps({'error':run.stderr}))
(out/'commands.json').write_text(json.dumps({'compile':cmd,'build_exit':build.returncode,'run':[str(out/'probe')],'run_exit':run.returncode},indent=2))
(out/'source-hashes.json').write_text(json.dumps({str(p):hashlib.sha256(p.read_bytes()).hexdigest() for p in [source/'identity_darwin.m',source/'input_darwin.m',fixture/'probe.m',fixture/'run.py']},indent=2))
print('build',build.returncode,'run',run.returncode)
sys.exit(int(build.returncode!=0 or run.returncode!=0))
