# -*- coding: utf-8 -*-
"""ReqWeave アプリアイコン（糸巻きの意匠）の SVG を生成する。

配色はアプリ画面のカラートークン（bg / accent 緑 / info 青 / warn 橙）に合わせる。

糸玉は「中心の穴から出た糸が玉を一周して穴へ戻る」巻き方であり、
その 1 本を「穴を端点に持つ楕円」で表し、角度を変えた族として重ねる。
線束の幅（膨らみ）を角度に対して滑らかに変えることで、乱雑な線の集まりではなく
規則的に巻かれた層として見せる。

  再生成（原本・PNG・ICO・manifest を一括で作り直す）:
    python3 app/build/icon/gen_appicon.py --build

  検査（原本と生成物が食い違っていないか。`make -C app lint` の iconcheck が呼ぶ）:
    python3 app/build/icon/gen_appicon.py --check

再生成には librsvg（rsvg-convert）と Pillow が要る。アプリのビルド・実行には不要
（配布物への依存は増えない）。道具の無い環境では検査は「未実施」として明示的に飛ばす
（黙って緑にしない）。
"""
import math, os, sys

S = 1024
CX, CY = S / 2, S / 2 + 12
R = 374
HOLE = (CX - 2, CY - 248)

BG, BG2, BORDER = "#16181d", "#232830", "#2e333b"

# 糸色（画面のカラートークンの accent 緑 / info 青 / warn 橙 を核に、濃淡を足したもの）を巡回させる。
# 3 色 × 3 階調を順に使うことで、参考画像の「多色に染め分けた糸」の見えを作る。
PALETTE = [
    "#5fc99a", "#7fd8ac", "#2f9e72",
    "#7fa3f0", "#9db9f6", "#3b79d4",
    "#d9a75f", "#ecc689", "#b8853a",
]

def petal(theta_deg, width, anchor):
    hx, hy = anchor
    th = math.radians(theta_deg)
    dx, dy = math.cos(th), math.sin(th)
    ox, oy = hx - CX, hy - CY
    rr = R * 0.99
    b = ox * dx + oy * dy
    c = ox * ox + oy * oy - rr * rr
    disc = b * b - c
    if disc <= 1.0:
        return ""
    t = -b + math.sqrt(disc)
    a = t / 2.0
    ex, ey = hx + dx * t, hy + dy * t
    return (f'<path d="M {hx:.1f} {hy:.1f} '
            f'A {a:.1f} {width:.1f} {theta_deg:.1f} 0 1 {ex:.1f} {ey:.1f} '
            f'A {a:.1f} {width:.1f} {theta_deg:.1f} 0 1 {hx:.1f} {hy:.1f}" />')

def build():
    o = []
    add = o.append
    add(f'<svg xmlns="http://www.w3.org/2000/svg" width="{S}" height="{S}" viewBox="0 0 {S} {S}">')
    add('<defs>')
    add(f'<clipPath id="ball"><circle cx="{CX}" cy="{CY}" r="{R}"/></clipPath>')
    add('<radialGradient id="shade" cx="34%" cy="24%" r="86%">'
        '<stop offset="0%" stop-color="#ffffff" stop-opacity="0.18"/>'
        '<stop offset="50%" stop-color="#ffffff" stop-opacity="0"/>'
        '<stop offset="100%" stop-color="#04070a" stop-opacity="0.46"/></radialGradient>')
    add('<linearGradient id="plate" x1="0" y1="0" x2="0.2" y2="1">'
        f'<stop offset="0%" stop-color="{BG2}"/><stop offset="100%" stop-color="{BG}"/></linearGradient>')
    add('<linearGradient id="base" x1="0.15" y1="0" x2="0.85" y2="1">'
        '<stop offset="0%" stop-color="#8fd9b4"/><stop offset="50%" stop-color="#a8c2f7"/>'
        '<stop offset="100%" stop-color="#e0b070"/></linearGradient>')
    add('</defs>')

    add(f'<rect width="{S}" height="{S}" rx="216" ry="216" fill="url(#plate)"/>')
    add(f'<rect x="5" y="5" width="{S-10}" height="{S-10}" rx="212" ry="212" fill="none" '
        f'stroke="{BORDER}" stroke-width="10"/>')
    add(f'<circle cx="{CX}" cy="{CY}" r="{R}" fill="url(#base)"/>')

    add('<g clip-path="url(#ball)" fill="none" stroke-linecap="round">')
    n = 72
    for i in range(n):
        theta = 90.0 + i * (360.0 / n)
        # 幅は角度に対して滑らかに変える（巻きの層が規則的に重なって見える）
        u = i / n * 2 * math.pi
        width = R * (0.30 + 0.30 * math.sin(u) * math.sin(u) + 0.12 * math.cos(2 * u))
        col = PALETTE[i % len(PALETTE)]
        anchor = (HOLE[0] + 12 * math.cos(u), HOLE[1] + 6 * math.sin(u))
        d = petal(theta, width, anchor)
        if d:
            add(f'<g stroke="{col}" stroke-width="16" stroke-opacity="0.97">{d}</g>')
    add('</g>')

    add(f'<circle cx="{CX}" cy="{CY}" r="{R}" fill="url(#shade)" clip-path="url(#ball)"/>')
    add(f'<circle cx="{CX}" cy="{CY}" r="{R}" fill="none" stroke="#0d1014" stroke-width="12" stroke-opacity="0.5"/>')

    hx, hy = HOLE
    add(f'<ellipse cx="{hx:.1f}" cy="{hy:.1f}" rx="68" ry="43" fill="#141820"/>')
    add(f'<ellipse cx="{hx:.1f}" cy="{hy:.1f}" rx="68" ry="43" fill="none" stroke="#0b0e12" stroke-width="9" stroke-opacity="0.85"/>')
    add('</svg>')
    return "\n".join(o)

# ---- 生成物の書き出しと検査 ------------------------------------

PNG_SIZE = 1024
ICO_SIZES = [256, 128, 64, 48, 32, 16]

HERE = os.path.dirname(os.path.abspath(__file__))
BUILD = os.path.dirname(HERE)                     # app/build
SVG_PATH = os.path.join(HERE, "appicon.svg")
PNG_PATH = os.path.join(BUILD, "appicon.png")
ICO_PATH = os.path.join(BUILD, "windows", "icon.ico")
MANIFEST_PATH = os.path.join(HERE, "icon.manifest.json")


def sha256_of(path):
    import hashlib
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


def render_png(svg_path, dst, size):
    import subprocess
    subprocess.run(["rsvg-convert", "-w", str(size), "-h", str(size), svg_path, "-o", dst], check=True)


def write_ico(svg_path, dst):
    """SVG から多サイズの .ico を作る（Windows のタスクバー・エクスプローラ用）。"""
    import subprocess, tempfile, os
    from PIL import Image

    images = []
    with tempfile.TemporaryDirectory() as tmp:
        for size in ICO_SIZES:
            png = os.path.join(tmp, f"{size}.png")
            render_png(svg_path, png, size)
            images.append(Image.open(png).convert("RGBA"))
        images[0].save(dst, format="ICO", sizes=[(s, s) for s in ICO_SIZES])


def write_manifest():
    """原本と生成物のハッシュを記録する。iconcheck（Go）はこれと実ファイルを突き合わせる。"""
    import json
    data = {
        "_comment": "gen_appicon.py --build が書き出す。手で編集しない。",
        "svg": sha256_of(SVG_PATH),
        "png": sha256_of(PNG_PATH),
        "ico": sha256_of(ICO_PATH),
        "png_size": PNG_SIZE,
        "ico_sizes": sorted(ICO_SIZES),
    }
    with open(MANIFEST_PATH, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
        f.write("\n")


def build_all():
    """原本 SVG → PNG → ICO → manifest を一括で作り直す（部分更新による食い違いを作らない）。"""
    with open(SVG_PATH, "w", encoding="utf-8") as f:
        f.write(build())
    render_png(SVG_PATH, PNG_PATH, PNG_SIZE)
    write_ico(SVG_PATH, ICO_PATH)
    write_manifest()
    print(f"生成しました: {SVG_PATH} / {PNG_PATH} / {ICO_PATH} / {MANIFEST_PATH}")


def check():
    """生成物が原本から作り直したものと一致するかを画素で比べる（0 = 一致）。

    バイト一致では librsvg のバージョン差で偽陽性になるため、画素差の最大値で判定する。
    """
    import tempfile
    from PIL import Image, ImageChops

    problems = []
    # 1. 原本 SVG が生成器の出力と一致するか（原本を手で書き換えていないか）
    expected_svg = build()
    with open(SVG_PATH, encoding="utf-8") as f:
        if f.read() != expected_svg:
            problems.append(
                "appicon.svg が生成器の出力と一致しません（原本を手で書き換えたか、生成器だけを変えています）")

    # 2. PNG / ICO が原本 SVG から作り直したものと一致するか（画素差で判定）
    with tempfile.TemporaryDirectory() as tmp:
        png = os.path.join(tmp, "appicon.png")
        render_png(SVG_PATH, png, PNG_SIZE)
        problems += diff_images(Image.open(png), Image.open(PNG_PATH), "appicon.png", ImageChops)

        ico = os.path.join(tmp, "icon.ico")
        write_ico(SVG_PATH, ico)
        got, want = Image.open(ICO_PATH), Image.open(ico)
        if sorted(got.ico.sizes()) != sorted(want.ico.sizes()):
            problems.append(f"icon.ico のサイズ構成が違います: {sorted(got.ico.sizes())}")
        else:
            for size in sorted(want.ico.sizes()):
                problems += diff_images(want.ico.getimage(size), got.ico.getimage(size),
                                        f"icon.ico {size[0]}px", ImageChops)

    if problems:
        print("✘ アイコンの原本と生成物が食い違っています:")
        for p in problems:
            print(f"  {p}")
        print("  → python3 app/build/icon/gen_appicon.py --build で作り直してください。")
        return 1
    print("iconcheck: 原本 SVG と生成物（appicon.png / icon.ico）は一致しています")
    return 0


# 画素差の許容（librsvg のバージョン差による微差を吸収する。構造の違いはこれを超える）
PIXEL_TOLERANCE = 16


def diff_images(want, got, label, ImageChops):
    if want.size != got.size:
        return [f"{label} の寸法が違います: {got.size}（期待 {want.size}）"]
    d = ImageChops.difference(want.convert("RGBA"), got.convert("RGBA"))
    worst = max(band.getextrema()[1] for band in d.split())
    if worst > PIXEL_TOLERANCE:
        return [f"{label} が原本から作り直したものと違います（画素差 最大 {worst}）"]
    return []


if __name__ == "__main__":
    if "--build" in sys.argv:
        build_all()
    elif "--check" in sys.argv:
        sys.exit(check())
    else:
        sys.stdout.write(build())
