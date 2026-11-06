#!/usr/bin/env python3
"""Build self-contained Windows EXEs and portable ZIPs with source identity."""
import argparse
import hashlib
import json
import os
import pathlib
import re
import shutil
import subprocess
import zipfile

ROOT = pathlib.Path(__file__).resolve().parents[1]

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--version', default='v0.4.0')
    ap.add_argument('--arch', nargs='+', choices=['amd64', 'arm64'], default=['amd64', 'arm64'])
    args = ap.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]{0,63}', args.version):
        raise ValueError('invalid release version')
    go = shutil.which('go') or '/usr/local/go/bin/go'
    commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    if subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT, text=True).strip():
        commit += '-dirty'
    destination = ROOT / 'release_assets' / ('windows-' + args.version)
    destination.mkdir(parents=True, exist_ok=True)
    manifest = dict(version=args.version, source_commit=commit,
                    go_version=subprocess.check_output([go, 'version'], text=True).strip(),
                    ui='embedded local web page opened in default browser',
                    windows_native_runtime_tested=False, artifacts=[])
    for arch in dict.fromkeys(args.arch):
        executable = destination / f'IPData-WEB-windows-{arch}.exe'
        env = dict(os.environ, CGO_ENABLED='0', GOOS='windows', GOARCH=arch)
        subprocess.run([go, 'build', '-trimpath', '-ldflags',
                        f'-s -w -X main.buildVersion={args.version} -X main.buildCommit={commit}',
                        '-o', str(executable), '.'], cwd=ROOT / 'combined_refactor', env=env, check=True)
        if executable.read_bytes()[:2] != b'MZ':
            raise ValueError('build did not produce a Windows executable')
        package = destination / f'IPData-WEB-windows-{arch}.zip'
        folder = f'IPData-WEB-windows-{arch}/'
        package_info = dict(version=args.version, source_commit=commit,
                            target=f'windows/{arch}', exe_sha256=hashlib.sha256(executable.read_bytes()).hexdigest())
        launcher = '\r\n'.join(['@echo off', 'chcp 65001 >nul', 'pushd "%~dp0"',
                                '"%~dp0IPData-WEB.exe" %*',
                                'if errorlevel 1 (', '  echo 启动失败，请查看上面的错误提示。',
                                '  pause', ')', 'popd', ''])
        readme = ('IPData-WEB ' + args.version + '\r\n\r\n'
                  '完整解压后双击 IPData-WEB.exe。自动打开浏览器，无需安装其他运行时。\r\n'
                  '任务数据保存在程序旁的 ipdata-tasks 文件夹。关闭浏览器不会退出程序。\r\n'
                  '退出建议先暂停任务，再在控制台按 Ctrl+C。\r\n'
                  '浏览器打不开时，访问控制台显示的网址。\r\n'
                  '完整说明见 windows.md；构建信息见 build-info.json。\r\n')
        with zipfile.ZipFile(package, 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=9) as z:
            z.write(executable, folder + 'IPData-WEB.exe')
            z.writestr(folder + 'Start-IPData.cmd', launcher.encode('utf-8'))
            z.writestr(folder + 'README-Windows.txt', ('\ufeff' + readme).encode('utf-8'))
            z.write(ROOT / 'docs/windows.md', folder + 'windows.md')
            z.write(ROOT / 'LICENSE', folder + 'LICENSE')
            z.write(ROOT / 'THIRD_PARTY_NOTICES.md', folder + 'THIRD_PARTY_NOTICES.md')
            z.writestr(folder + 'build-info.json', json.dumps(package_info, indent=2) + '\n')
        with zipfile.ZipFile(package) as z:
            if z.testzip() is not None:
                raise ValueError('archive validation failed')
            if z.read(folder + 'IPData-WEB.exe') != executable.read_bytes():
                raise ValueError('archive executable differs from build output')
        for artifact in [executable, package]:
            manifest['artifacts'].append(dict(file=artifact.name, bytes=artifact.stat().st_size,
                                             sha256=hashlib.sha256(artifact.read_bytes()).hexdigest()))
    (destination / 'build-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n', encoding='utf-8')
    (destination / 'SHA256SUMS.txt').write_text(''.join(f'{a["sha256"]}  {a["file"]}\n' for a in manifest['artifacts']), encoding='ascii')
    print(json.dumps(manifest, indent=2))

if __name__ == '__main__':
    main()
