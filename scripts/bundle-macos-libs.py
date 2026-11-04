#!/usr/bin/env python3
"""把 macOS 二进制依赖的非系统动态库（FreeRDP / WinPR / OpenSSL 等）复制到 Frameworks 目录，
并改写 install name，使其不再指向 /opt/homebrew。docs/packaging.md §4。

用法：
  python3 -I scripts/bundle-macos-libs.py --binary app/src-tauri/binaries/rdp-worker-aarch64-apple-darwin \
      --out app/src-tauri/frameworks [--conf app/src-tauri/tauri.macos.conf.json]

做了什么：
  1. 递归解析依赖（otool -L，支持 @rpath/@loader_path/@executable_path）。
  2. 平铺复制到 --out；每个 dylib 的 id 改为 @rpath/<name>，依赖改为 @rpath/<name>，并加 rpath @loader_path。
  3. 主二进制的依赖改为 @rpath/<name>，加 rpath @executable_path/../Frameworks（.app/Contents/MacOS -> Contents/Frameworks）。
  4. 对被修改的文件做 ad-hoc 重签名（install_name_tool 会使签名失效，arm64 上未签名的二进制会被系统杀掉）。
  5. 校验：任何文件仍引用非系统绝对路径则失败。
  6. （可选）生成 Tauri 的 tauri.macos.conf.json，把 --out 里的库放进 Contents/Frameworks 并使用 ad-hoc 签名。
"""
from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

SYSTEM_PREFIXES = ("/usr/lib/", "/System/", "/Library/Apple/")
BAD_PREFIXES = ("/opt/homebrew", "/usr/local", "/Users/", "/nix/", "/opt/local")


def run(*cmd: str) -> str:
    return subprocess.run(cmd, check=True, capture_output=True, text=True).stdout


def is_system(dep: str) -> bool:
    return dep.startswith(SYSTEM_PREFIXES)


def deps_of(path: Path) -> list[str]:
    """otool -L 列出的依赖（去掉 dylib 自身的 id 行）。"""
    lines = run("otool", "-L", str(path)).splitlines()[1:]
    out = []
    for line in lines:
        m = re.match(r"\s+(\S+) \(", line)
        if m:
            out.append(m.group(1))
    ident = dylib_id(path)
    return [d for d in out if d != ident]


def dylib_id(path: Path) -> str | None:
    lines = run("otool", "-D", str(path)).splitlines()
    return lines[1].strip() if len(lines) > 1 else None


def rpaths_of(path: Path) -> list[str]:
    out = run("otool", "-l", str(path)).splitlines()
    rp = []
    for i, line in enumerate(out):
        if line.strip() == "cmd LC_RPATH":
            for nxt in out[i + 1 : i + 4]:
                m = re.match(r"\s+path (.+) \(offset", nxt)
                if m:
                    rp.append(m.group(1))
    return rp


def resolve(dep: str, loader: Path, main_dir: Path, rpaths: list[str]) -> Path | None:
    def expand(p: str) -> Path:
        p = p.replace("@loader_path", str(loader.parent)).replace("@executable_path", str(main_dir))
        return Path(p)

    if dep.startswith("@rpath/"):
        rest = dep[len("@rpath/") :]
        for rp in rpaths:
            cand = expand(rp) / rest
            if cand.exists():
                return cand.resolve()
        return None
    if dep.startswith(("@loader_path/", "@executable_path/")):
        cand = expand(dep)
        return cand.resolve() if cand.exists() else None
    cand = Path(dep)
    return cand.resolve() if cand.exists() else None


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--binary", required=True, type=Path, help="要处理的二进制（原地修改）")
    ap.add_argument("--out", required=True, type=Path, help="Frameworks 输出目录")
    ap.add_argument("--conf", type=Path, help="生成 tauri.macos.conf.json 的路径")
    args = ap.parse_args()

    binary: Path = args.binary.resolve()
    out: Path = args.out.resolve()
    if sys.platform != "darwin":
        print("bundle-macos-libs: only for macOS", file=sys.stderr)
        return 1
    if out.exists():
        shutil.rmtree(out)
    out.mkdir(parents=True)

    # name -> 复制后的路径；dep 字符串 -> name（同一个库可能被不同路径引用）
    copied: dict[str, Path] = {}
    queue: list[tuple[Path, Path]] = [(binary, binary)]  # (原始路径, 当前文件路径)
    seen_real: dict[Path, str] = {}
    changes: dict[Path, list[tuple[str, str]]] = {}  # 文件 -> [(旧依赖, 新依赖)]

    while queue:
        orig, cur = queue.pop()
        loader_rpaths = rpaths_of(orig) + ([] if orig == binary else rpaths_of(binary))
        for dep in deps_of(orig):
            if is_system(dep):
                continue
            real = resolve(dep, orig, binary.parent, loader_rpaths)
            if real is None:
                print(f"error: cannot resolve {dep} (needed by {orig})", file=sys.stderr)
                return 1
            if is_system(str(real)):
                continue
            name = seen_real.get(real) or os.path.basename(dep)
            if real not in seen_real:
                # 同名不同文件的冲突检查
                if name in copied:
                    print(f"error: name clash for {name}: {real} vs {copied[name]}", file=sys.stderr)
                    return 1
                dest = out / name
                shutil.copy2(real, dest)
                dest.chmod(0o755)
                copied[name] = dest
                seen_real[real] = name
                queue.append((real, dest))
            changes.setdefault(cur, []).append((dep, f"@rpath/{name}"))

    # 改写 install name
    for name, path in copied.items():
        run("install_name_tool", "-id", f"@rpath/{name}", str(path))
        run("install_name_tool", "-add_rpath", "@loader_path", str(path))
    for path, lst in changes.items():
        for old, new in lst:
            run("install_name_tool", "-change", old, new, str(path))
    run("install_name_tool", "-add_rpath", "@executable_path/../Frameworks", str(binary))

    # 重新签名（ad-hoc）
    for path in [*copied.values(), binary]:
        run("codesign", "--force", "--sign", "-", str(path))

    # 校验：不能再引用任何非系统绝对路径
    bad = []
    for path in [*copied.values(), binary]:
        for dep in deps_of(path) + ([dylib_id(path)] if path != binary else []):
            if dep and (dep.startswith(BAD_PREFIXES) or (dep.startswith("/") and not is_system(dep))):
                bad.append(f"{path.name}: {dep}")
    if bad:
        print("error: leftover non-system references:\n  " + "\n  ".join(bad), file=sys.stderr)
        return 1

    print(f"bundled {len(copied)} libraries into {out}:")
    for name in sorted(copied):
        print(f"  {name}")

    if args.conf:
        conf = {
            "bundle": {
                "macOS": {
                    "signingIdentity": "-",
                    "files": {f"Frameworks/{n}": f"frameworks/{n}" for n in sorted(copied)},
                }
            }
        }
        args.conf.write_text(json.dumps(conf, indent=2) + "\n")
        print(f"wrote {args.conf}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
