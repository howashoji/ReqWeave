# Build Directory

## ReqWeave のアプリアイコン

意匠は糸巻き（糸玉）。**原本は `icon/appicon.svg`** で、配布用の 2 ファイルはそこから生成する。
PNG / ICO を直接編集しないこと（原本と食い違う）。

| ファイル | 用途 |
|---|---|
| `icon/appicon.svg` | 原本（生成器 `icon/gen_appicon.py` の出力） |
| `icon/icon.manifest.json` | 原本・生成物のハッシュ記録（`iconcheck` が照合する。手で編集しない） |
| `appicon.png` | Wails のアイコン元（macOS の `.icns` はビルド時にここから生成される。1024×1024） |
| `windows/icon.ico` | Windows の実行ファイル・インストーラ（16/32/48/64/128/256px） |

**変更するときは 4 ファイルを一括で作り直す**（部分更新をしない）:

```
python3 build/icon/gen_appicon.py --build
```

同期は検証ゲートの lint 段（`go run ./tools/iconcheck`）が機械検査する。
ハッシュ・寸法・サイズ構成の照合は常に走り、再描画の突き合わせは道具がある環境でだけ走る
（無ければ「未実施」と表示して緑にする）。手元で突き合わせだけ行うには:

```
python3 build/icon/gen_appicon.py --check
```

生成・再描画の突き合わせに使う道具（`rsvg-convert` = librsvg、Python の Pillow）は**アイコンを触るときだけ**
必要で、アプリのビルド・実行には不要（配布物への依存は増えない）。

---


The build directory is used to house all the build files and assets for your application. 

The structure is:

* bin - Output directory
* darwin - macOS specific files
* windows - Windows specific files

## Mac

The `darwin` directory holds files specific to Mac builds.
These may be customised and used as part of the build. To return these files to the default state, simply delete them
and
build with `wails build`.

The directory contains the following files:

- `Info.plist` - the main plist file used for Mac builds. It is used when building using `wails build`.
- `Info.dev.plist` - same as the main plist file but used when building using `wails dev`.

## Windows

The `windows` directory contains the manifest and rc files used when building with `wails build`.
These may be customised for your application. To return these files to the default state, simply delete them and
build with `wails build`.

- `icon.ico` - The icon used for the application. This is used when building using `wails build`. If you wish to
  use a different icon, simply replace this file with your own. If it is missing, a new `icon.ico` file
  will be created using the `appicon.png` file in the build directory.
- `installer/*` - The files used to create the Windows installer. These are used when building using `wails build`.
- `info.json` - Application details used for Windows builds. The data here will be used by the Windows installer,
  as well as the application itself (right click the exe -> properties -> details)
- `wails.exe.manifest` - The main application manifest file.