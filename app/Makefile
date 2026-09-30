# ReqWeave のビルドと検証ゲート
#
# 検証ゲートは build → lint → test-unit → test-layout → test-integration → test-e2e の 6 段。
# 段ごとに `make -C app <段>` で実行し、前の段が通ってから次の段へ進む。
#
#   make build            アプリ本体（.app / .exe）を生成する
#   make dist             配布用の書庫（版番号入り dmg + 初回起動の手順）を生成する（未署名）
#   make dist-release     署名・公証つきの macOS 配布物を生成する（リリース時のみ。要ネットワーク）
#   make dist-release-notarize  既存の配布物の公証だけをやり直す（dist-release が公証で失敗したときの再開点）
#   make dist-windows     Windows 向けの配布用書庫を生成する（macOS ホストからのクロスビルドで生成できる）
#   make lint             gofmt / go vet / 依存規則 / アイコンの同期 / 版番号の一致 / TypeScript 型検査
#   make version          版番号の正本（internal/appversion/VERSION）を表示する
#   make test-unit        Go・フロントエンドのユニットテスト
#   make test-layout      実ブラウザ（Playwright/Chromium）でのレイアウト検査
#   make test-integration モジュール間・実ファイル I/O・ビルド成果物の結合テスト
#                         （時間で判定するテストは、他が終わった後に単独で回す）
#   make test-e2e         配布物の実機確認（起動・起動時間・universal・自動更新。dist に依存）
#   make dev              開発サーバ（ホットリロード）
#   make generate         Wails バインディングの再生成
#   make clean            ビルド生成物とゲートログの削除

SHELL       := /bin/bash
GOBIN       := $(shell go env GOPATH)/bin
WAILS       := $(GOBIN)/wails
FRONTEND    := frontend
NPM         := npm --prefix $(FRONTEND)
NODE_STAMP  := $(FRONTEND)/node_modules/.package-lock.json

# 版番号の正本は internal/appversion/VERSION の 1 ファイルだけ。
# Go の実行時値は go:embed で同じファイルから得るため、ここは配布物名など
# make 側で版番号を必要とする箇所のための読み出しに限る。
VERSION_FILE := internal/appversion/VERSION
APP_VERSION  := $(shell tr -d ' \t\r\n' < $(VERSION_FILE))

# 配布対象は macOS / Windows。Linux は開発ホストとしてのみ支援する。
# Wails v2 の Linux ビルドは既定で webkit2gtk-4.0 を要求するが、近年のディストリ
# （Ubuntu 24.04 等）は 4.1 しか提供しない。Wails 側のビルドタグ webkit2_41 で吸収する。
# macOS / Windows ホストではタグを付けない（ビルドコマンドは従来どおり）。
BUILD_TAGS  :=
ifeq ($(shell uname -s),Linux)
BUILD_TAGS  := $(shell pkg-config --exists webkit2gtk-4.0 2>/dev/null || echo webkit2_41)
endif
WAILS_TAGS  := $(if $(BUILD_TAGS),-tags $(BUILD_TAGS),)

.PHONY: build build-universal codex dist dist-release dist-release-notarize dist-release-verify print-sign-identity dist-windows lint lint-go lint-frontend lint-deps lint-icon lint-version lint-notice test-sharedfs \
        test-unit test-unit-go test-unit-frontend test-layout test-integration test-e2e dev generate clean version

# 結合テスト・e2e で使う Go のビルドタグ（tag 付きファイルも vet の対象にする）
TEST_TAGS   := integration e2e
FRONTEND_DIST := $(FRONTEND)/dist/index.html

# ---- 依存 ----------------------------------------------------------------

$(NODE_STAMP): $(FRONTEND)/package.json $(FRONTEND)/package-lock.json
	$(NPM) ci

# ---- build ---------------------------------------------------------------

# **-trimpath を必ず付ける**。付けないと Go がソースの絶対パスを実行ファイルへ埋め込み、
# **ビルドしたマシンの利用者名とディレクトリ構成が配布物に混入する**
# （2026-09-04 実測: `/Users/<利用者名>` が 1940 件。付けると 0 件になる）。
# 埋め込まれるビルド情報（CGO_CXXFLAGS）にも同じパスが入るが、これも -trimpath で消える。
build: $(NODE_STAMP) codex
	$(WAILS) build -trimpath $(WAILS_TAGS)
	@$(call embed_codex,build/bin/ReqWeave.app)
	@$(call embed_legal,build/bin/ReqWeave.app)
	@$(call reseal_adhoc,build/bin/ReqWeave.app)

# ---- codex（同梱する Codex App Server の本体）--------------------------------
#
# 版・取得元・アーキテクチャごとの SHA-256 は internal/aiprovider/adapter/codex/bundle.json が正本。
# 取得物はハッシュで照合してから universal binary に組み立てる。
# **一度用意すれば以後は何もしない**（取得はネットワークが要るため、ゲートを毎回ネットワーク必須にしない）。
# 版を上げるときは定義を書き換えてから `go run ./tools/codexfetch -force`。
CODEX_DIR := build/codex
CODEX_BIN := $(CODEX_DIR)/codex

codex:
	@go run ./tools/codexfetch -out $(CODEX_DIR)

# embed_codex はアプリ本体の中へ同梱物を収める（実行ファイル＋ライセンス表示）。
# 置き場所の正本はアダプタ（codex.BundledExecutableRelPath）で、配布物の検査（distcheck）も同じ場所を見る。
define embed_codex
mkdir -p "$(1)/Contents/Resources/codex" && \
ditto "$(CODEX_BIN)" "$(1)/Contents/Resources/codex/codex" && \
ditto "$(CODEX_DIR)/LICENSE" "$(1)/Contents/Resources/codex/LICENSE" && \
ditto "$(CODEX_DIR)/NOTICE" "$(1)/Contents/Resources/codex/NOTICE"
endef

# embed_legal はアプリ本体の中へライセンス表示（リポジトリ直上の LICENSE・NOTICE）を収める。
# 第三者のソフトウェアの MIT・BSD などは、複製物にライセンス表示を含めることを条件にしている。
# アプリ本体の中に置くので、利用者がアプリを移しても・自動更新で入れ替えても一緒に付いていく。
# 配布物の検査（distcheck）が原本と 1 バイトも違わないことを見る。
LEGAL_FILES := ../LICENSE ../NOTICE
define embed_legal
mkdir -p "$(1)/Contents/Resources" && \
cp $(LEGAL_FILES) "$(1)/Contents/Resources/"
endef

# reseal_adhoc は同梱物を入れた後に、アプリ本体の ad-hoc 署名をやり直す。
# Wails はビルドの最後に ad-hoc 署名をするので、その後に入れた同梱物（Codex・ライセンス表示）は
# 署名の封（sealed resources）から外れ、codesign --verify が「a sealed resource is missing or invalid」になる
# （前回のビルドの残りがあると封に入って見過ごす）。--deep を付けない（同梱の Codex の Developer ID 署名を保つ）。
# リリース物は dist-release が Developer ID で署名し直すので、ここは開発・検証ゲート用の配布物のため。
define reseal_adhoc
codesign --force --sign - "$(1)" && codesign --verify --strict "$(1)"
endef

# ---- dist（配布用の書庫）----------------------------------------------------
#
# 配布物は **OS ごとに 1 個**。中身はアプリ本体 1 個と
# 初回起動の手順書（OS の警告を越えて起動する手順）とライセンス表示（macOS はアプリ本体の中、Windows は zip の直下）。
# **macOS は dmg、Windows は zip**。
# アップデータは配布物からアプリ本体を選んで差し替える（internal/updater）。
DIST_DIR    := build/dist
PACKAGE_DIR := build/package
# **ゲート用の配布物には名前で印を付ける**。リリース物（$(MAC_RELEASE_DMG)）と
# 同名にすると、コピー・アップロードの段で人が取り違える（2026-09-04 に実際に発生し、
# 未署名の配布物が公開された）。**リリース物の名前は変えられない**（更新マニフェストの URL が
# ファイル名で固定されるため）ので、**ゲート用の側の名前を変える**。
MAC_DMG     := $(DIST_DIR)/ReqWeave-$(APP_VERSION)-macos-unsigned.dmg
WIN_ZIP     := $(DIST_DIR)/ReqWeave-$(APP_VERSION)-windows.zip

# **リリース物は別の場所へ置く**。配布物名は変えられない（更新マニフェストの URL が
# ファイル名で固定されるため）ので、置き場所で分ける。同じパスにすると、検証ゲートの
# `test-e2e`（= dist に依存）が**署名・公証済みの配布物を未署名で上書きしてしまう**。
# 公証は Apple の審査待ちで数時間かかることがあり（2026-09-03 実測 = 3 時間超）、
# 失うと再ビルド・再公証からやり直しになる。dist と dist-windows が互いの配布物を消さないようにしているのと同じ理由。
RELEASE_DIR := build/release
MAC_RELEASE_DMG := $(RELEASE_DIR)/ReqWeave-$(APP_VERSION)-macos.dmg
# **作りかけは別名にし、全段（署名・公証・ステープル・検査）が通ってから置き換える**。
# 先に $(MAC_RELEASE_DMG) を消してから作り始めると、途中で失敗したときに
# **公開中の配布物と同一の実体を失う**（2026-09-04 に実際に消えた。作り直しには再公証が要る）。
MAC_RELEASE_TMP := $(RELEASE_DIR)/.building-ReqWeave-$(APP_VERSION)-macos.dmg
# 公証・ステープル・検査の対象。dist-release は作りかけを渡し、再開点は公開候補そのものを見る。
NOTARY_TARGET ?= $(MAC_RELEASE_DMG)
# dmg を作るための一時的な中身の置き場（作成後に消す）。
DMG_ROOT    := $(DIST_DIR)/dmgroot

# macOS は Apple Silicon・Intel の双方で動く Universal Binary を作る。
build-universal: $(NODE_STAMP) codex
	$(WAILS) build -trimpath -platform darwin/universal $(WAILS_TAGS)
	@$(call embed_codex,build/bin/ReqWeave.app)
	@$(call embed_legal,build/bin/ReqWeave.app)
	@$(call reseal_adhoc,build/bin/ReqWeave.app)

# **自分の成果物だけを消す**（`rm -rf $(DIST_DIR)` にすると、もう一方の OS の配布物まで
# 消えて両 OS の配布物を同時に持てない。リリースは両方を同時に必要とする）。
#
# **/Applications へのシンボリックリンクを dmg に入れない**。
# 一般的な dmg は「アプリを /Applications へドラッグ」を促すが、本アプリは
# **ホームフォルダ配下**に置かないと自動更新ができない（同梱の手順書が警告している）。
# 入れると手順書と矛盾した誘導になるため置かない。
dist: build-universal
	# **版を問わず**ゲート用の配布物を消してから作る。現行版の名前だけを消すと、
	# 版を上げたときに旧版の配布物が残り、e2e（置き場の配布物を 1 個に定めて使う）が
	# 「配布物が 1 個に定まりません（2 件）」で全件止まる（2026-09-10 に 0.1.0 → 0.1.1 で発生）。
	@mkdir -p $(DIST_DIR) && rm -f $(DIST_DIR)/ReqWeave-*-macos-unsigned.dmg
	# **リリース物と同名の残骸をゲート用の置き場から消す**。
	# 名前を分ける前に作られた `ReqWeave-<版>-macos.dmg` が残っていると、
	# 置き場所を見ずに名前だけで拾ったときに未署名の配布物を掴む。版を問わない。
	@rm -f $(DIST_DIR)/ReqWeave-*-macos.dmg
	@rm -rf $(DMG_ROOT) && mkdir -p $(DMG_ROOT)
	@cp -R build/bin/ReqWeave.app $(DMG_ROOT)/
	@cp $(PACKAGE_DIR)/*.md $(DMG_ROOT)/
	@hdiutil create -volname ReqWeave -srcfolder $(DMG_ROOT) -ov -format UDZO -quiet $(MAC_DMG)
	@rm -rf $(DMG_ROOT)
	@go run ./tools/distcheck $(MAC_DMG) darwin
	@echo "配布物: $(MAC_DMG)（版 $(APP_VERSION)）"

# ---- dist-release（署名・公証つきの配布物）---------------------------------
#
# **リリースのときだけ**実行する。dist と分けてある理由:
#
#   - `codesign --timestamp`（公証の必須条件）は Apple のタイムスタンプサーバへ出る。
#     dist に混ぜると **test-e2e が dist に依存しているため検証ゲート全体がネットワーク必須**になる。
#   - `notarytool submit --wait` は数分かかる。ゲートを回すたびに数分増える。
#   - リリースでもないのに Apple のサービスへ提出が飛ぶ。
#
# 段の順序（この順でないと成立しない）:
#   1. .app へ署名（hardened runtime + タイムスタンプ）
#   2. .app を公証へ出し、**.app へステープル**（利用者が dmg から取り出した .app が
#      オフラインでも検証できるようにする。ここを飛ばすと dmg の外へ出た時点で検証できない）
#   3. ステープル済みの .app から dmg を作り、dmg へ署名
#   4. **dmg を**公証へ出し、**dmg へステープル**
#   5. distcheck -require-signature で検査（署名・公証が欠けていれば不合格）
#
# 署名 ID はキーチェーンから引く（リポジトリに書かない）。秘密鍵はキーチェーンから出ず、
# 公証の資格情報は --keychain-profile 経由で渡すため、**秘密は引数にも環境変数にも現れない**
# （秘密をログやプロセスの一覧に出さないため）。事前に Developer ID Application の証明書をキーチェーンへ入れ、
# 公証の資格情報を `xcrun notarytool store-credentials "ReqWeave-Notary"` で登録しておく。
#
# **codesign へは証明書の SHA-1 を渡す**（名前は渡さない）。同じ名前の証明書がキーチェーンに 2 枚あると
# （作り直した証明書など）、名前では codesign が ambiguous で止まる。SHA-1 は証明書の指紋で、秘密ではない。
#   - MAC_SIGN_IDENTITY=<SHA-1> を渡せば、その証明書を使う（名前も受けるが、同じ名前が 2 枚あれば止まる）。
#   - 渡さなければ、Developer ID Application の証明書が 1 枚だけのときにそれを使う。
#     **2 枚以上なら選ばずに止まり、候補の SHA-1 と名前を出す**（1 枚目を黙って使うと、別の製品や
#     古い証明書で署名するおそれがある）。決め方は tools/signidentity（試験は同じ場所）。
#   - 公証まで走らせずに、どの証明書に決まるかだけを確かめる: make print-sign-identity
#
# **遅延評価（:= ではなく =）にしてある**。dist 等、署名しないターゲットでは security を実行しない。
# 1 回の make の中では最初に参照したときに 1 回だけ決め、その値を使い回す（参照のたびに決め直すと、
# 止まったときの案内が参照の数だけ繰り返し出る）。
MAC_SIGN_IDENTITY ?=
MAC_SIGN_SHA1 = $(eval MAC_SIGN_SHA1 := $$(shell security find-identity -v -p codesigning 2>/dev/null | go run ./tools/signidentity -identity '$$(MAC_SIGN_IDENTITY)'))$(MAC_SIGN_SHA1)
MAC_NOTARY_PROFILE ?= ReqWeave-Notary
NOTARY_ZIP := $(DIST_DIR)/notarize-$(APP_VERSION).zip

# **公証の資格情報がいま読めるかを先に確かめる**。
# 画面ロック中はデータ保護キーチェーンを読めず、**ビルドと署名を終えたあとの提出で必ず失敗する**
# （2026-09-03 / 09-04 に 2 回発生。数分が無駄になり、署名済みの中間物が残る）。
# 文書の注意書きだけでは防げなかったため、段として持つ。プロファイル未作成・未取り込みも同時に検出する。
define require_notary
xcrun notarytool history --keychain-profile "$(MAC_NOTARY_PROFILE)" >/dev/null 2>&1 || { \
	echo "✘ 公証の資格情報を読めません（プロファイル: $(MAC_NOTARY_PROFILE)）"; \
	echo "  画面がロックされていると読めません。ロックを解除してから実行してください"; \
	echo "  （ロック中なら資格情報は消えていません。未登録のときは xcrun notarytool store-credentials \"$(MAC_NOTARY_PROFILE)\" で登録してください）"; \
	exit 1; }
endef

# 公証へ出して**受理されたことまで**確かめる。notarytool の終了コードだけでは足りない
# （提出そのものは成功し、審査結果が Invalid ということがある）。
define notarize
out=$$(xcrun notarytool submit --keychain-profile "$(MAC_NOTARY_PROFILE)" --wait "$(1)" 2>&1); \
echo "$$out"; \
echo "$$out" | grep -q "status: Accepted" || { echo "✘ 公証が受理されませんでした: $(1)"; exit 1; }
endef

dist-release: build-universal
	@test -n "$(MAC_SIGN_SHA1)" || { echo "✘ 署名 ID が決まらないため止めます（理由は上に出ています）"; exit 1; }
	@$(call require_notary)
	@mkdir -p $(RELEASE_DIR) $(DIST_DIR) && rm -f $(MAC_RELEASE_TMP) $(NOTARY_ZIP)
	@rm -rf $(DMG_ROOT) && mkdir -p $(DMG_ROOT)
	@cp -R build/bin/ReqWeave.app $(DMG_ROOT)/
	@cp $(PACKAGE_DIR)/*.md $(DMG_ROOT)/
	@codesign --force --options runtime --timestamp --sign "$(MAC_SIGN_SHA1)" $(DMG_ROOT)/ReqWeave.app
	@codesign --verify --strict --verbose=2 $(DMG_ROOT)/ReqWeave.app
	@ditto -c -k --keepParent $(DMG_ROOT)/ReqWeave.app $(NOTARY_ZIP)
	@$(call notarize,$(NOTARY_ZIP))
	@xcrun stapler staple $(DMG_ROOT)/ReqWeave.app
	@rm -f $(NOTARY_ZIP)
	@hdiutil create -volname ReqWeave -srcfolder $(DMG_ROOT) -ov -format UDZO -quiet $(MAC_RELEASE_TMP)
	@rm -rf $(DMG_ROOT)
	@codesign --force --timestamp --sign "$(MAC_SIGN_SHA1)" $(MAC_RELEASE_TMP)
	@$(MAKE) dist-release-notarize NOTARY_TARGET=$(MAC_RELEASE_TMP)
	@$(MAKE) dist-release-verify

# どの証明書で署名するかだけを確かめる（ビルド・署名・公証はしない。Apple へは何も送らない）。
# 決まれば SHA-1 を出し、決まらなければ dist-release と同じ理由で止まる。
print-sign-identity:
	@test -n "$(MAC_SIGN_SHA1)" || { echo "✘ 署名 ID が決まりません（理由は上に出ています）"; exit 1; }
	@echo "MAC_SIGN_SHA1 = $(MAC_SIGN_SHA1)"

# **再開点**（2026-09-03 実測）: 公証は数時間かかることがある（1 回目の .app が 3 時間超）。
# dmg の公証で失敗したときに、ビルドと **.app の公証をやり直さない**ための段。
# 例: 画面ロック中は公証の資格情報を読めず必ず失敗する。
# 既にある $(MAC_DMG)（中の .app は署名・公証・ステープル済み）へ、公証とステープルだけを行う。
dist-release-notarize:
	@test -f $(NOTARY_TARGET) || { echo "✘ 配布物がありません: $(NOTARY_TARGET)（先に make dist-release を実行する）"; exit 1; }
	@$(call require_notary)
	@$(call notarize,$(NOTARY_TARGET))
	@xcrun stapler staple $(NOTARY_TARGET)
	@go run ./tools/distcheck -require-signature $(NOTARY_TARGET) darwin
	@echo "公証とステープルが完了しました: $(NOTARY_TARGET)"

# **公証・ステープルまで終わった作りかけを、検査して公開候補にする段**。
# 最終段（検査・置き換え）だけが落ちたときに、**公証をやり直さずに再開する**ための入口。
dist-release-verify:
	@test -f $(MAC_RELEASE_TMP) || { echo "✘ 作りかけの配布物がありません: $(MAC_RELEASE_TMP)（先に make dist-release を実行する）"; exit 1; }
	@go run ./tools/distcheck -require-signature $(MAC_RELEASE_TMP) darwin
	@mv -f $(MAC_RELEASE_TMP) $(MAC_RELEASE_DMG)
	@echo "配布物（署名・公証つき）: $(MAC_RELEASE_DMG)（版 $(APP_VERSION)）"

# Windows 向け。**macOS からクロスビルドできる**（CGO 不要。実際に確かめてある）。
# 生成した .exe の動作は、ここでは確かめない（Windows の実機で別に確かめる）。
#
# -webview2 browser: WebView2 ランタイムが未導入だった場合に**入手先を利用者のブラウザで開く**。
# 既定の download / embed は「アプリ自身が Microsoft のサーバへ取得しに行く」ため
# アプリの通信先を限る方針に 4 つ目の経路が加わってしまう。対象は Windows 11 のみで、
# Evergreen ランタイムは OS に同梱されるため通常はこの経路に入らない。
dist-windows: $(NODE_STAMP)
	$(WAILS) build -trimpath -platform windows/amd64 -webview2 browser $(WAILS_TAGS)
	# 版を問わず Windows の配布物を消してから作る（dist と同じ理由）。
	# macOS 側の配布物には触れない（両 OS の配布物を同時に持てるようにする）。
	@mkdir -p $(DIST_DIR) && rm -f $(DIST_DIR)/ReqWeave-*-windows.zip
	@cp build/bin/ReqWeave.exe $(DIST_DIR)/
	@cp $(PACKAGE_DIR)/*.md $(DIST_DIR)/
	# ライセンス表示は zip の直下（実行ファイル 1 個の配布なので、中に入れる場所が無い）。
	@cp $(LEGAL_FILES) $(DIST_DIR)/
	@cd $(DIST_DIR) && zip -qry $(notdir $(WIN_ZIP)) ReqWeave.exe *.md LICENSE NOTICE
	@cd $(DIST_DIR) && rm -f ReqWeave.exe *.md LICENSE NOTICE
	@go run ./tools/distcheck $(WIN_ZIP) windows
	@echo "配布物: $(WIN_ZIP)（版 $(APP_VERSION)）"

# ---- lint ----------------------------------------------------------------

lint: lint-go lint-deps lint-icon lint-version lint-notice lint-frontend

# gofmt は差分ゼロを要求する（一覧が出た時点で失敗）
lint-go:
	@fmt=$$(gofmt -l .); \
	if [ -n "$$fmt" ]; then echo "✘ gofmt 未整形:"; echo "$$fmt"; exit 1; fi
	go vet ./...
	go vet -tags "$(TEST_TAGS)" ./...

# モジュール間の依存規則の機械検知
lint-deps:
	go run ./tools/depcheck

# アイコンの原本（SVG）と生成物（PNG / ICO）の同期。
# ハッシュ・寸法・サイズ構成の照合は常に実行し、再描画の突き合わせは
# 道具（librsvg / Pillow）がある環境でだけ行う（無い環境では未実施と明示する）。
lint-icon:
	go run ./tools/iconcheck

# 版番号の正本（VERSION）と複製（wails.json の productVersion）の一致。
# 版番号を上げ忘れたまま配布物を作ることを lint 段で止める。
lint-version:
	go run ./tools/versioncheck

# 第三者のライセンス文（リポジトリ直上の NOTICE）が依存と一致しているか。
# 依存（go.mod・package-lock.json）を変えたら go run ./tools/noticegen で作り直してコミットする。
# npm のパッケージのライセンス文は node_modules から読むので、npm ci の後に回す。
lint-notice: $(NODE_STAMP)
	go run ./tools/noticegen -check

lint-frontend: $(NODE_STAMP)
	$(NPM) run typecheck

# ---- test ----------------------------------------------------------------

test-unit: test-unit-go test-unit-frontend

test-unit-go:
	# -count=1 を外さない。Go のテストキャッシュは Go 以外のファイル
	#（Makefile・スクリプト等）の変更を鍵に含めないため、
	# それらを読むテストが壊れても cached で緑になる。
	go test -count=1 ./...

test-unit-frontend: $(NODE_STAMP)
	$(NPM) run test:unit

# レイアウト検査: **実ブラウザ**で描いて寸法を見る。
# jsdom はレイアウトを計算しないため、横のはみ出し・縦の押し出しは単体テストでは検出できない。
# 対象は `src/**/*.layout.test.tsx` のみ（起動が重いので全部は移さない。実測 1 秒程度）。
#
# **前提**: Playwright の Chromium。無い環境では失敗する（黙って緑にしない）。
# 用意する: cd frontend && npx playwright install chromium
test-layout: $(NODE_STAMP)
	$(NPM) run test:layout

# 結合: 実ファイル I/O・モジュール間・ビルド成果物（frontend/dist）を対象にする。
# 埋め込み対象が必要なため frontend のビルドに依存する。
#
# 2 回に分けて回す。
#   1 回目: TIMED_TESTS 以外の全部（パッケージを並列に回す）
#   2 回目: TIMED_TESTS だけを、1 回目が終わった後に -p 1 で単独に回す
# TIMED_TESTS は経過時間を持ち分・上限と比べて判定するテスト。全体の並列実行と CPU を
# 奪い合うと、実装と関係のない負荷で時間が伸びて落ちる（上限規模の画面応答の計測で、
# 単独なら 609ms の 1 回目が並列実行中に 2164ms になった）。持ち分は緩めず、計測の条件を揃える。
# 名前は全パッケージを通して一意であること（-skip と -run は全パッケージに同じ正規表現で効く）。
# 足すときは TIMED_PKGS にそのテストのパッケージも足す（足さないと 2 回目で実行されない）。
TIMED_TESTS := ^(TestScaleResponse|TestCrossProjectUsageTimesOutOneProject|TestWithShortLockWaitsThenTimesOut)$$
TIMED_PKGS  := ./internal/binding/ ./internal/projectstore/

test-integration: $(FRONTEND_DIST)
	go test -tags integration -count=1 -skip '$(TIMED_TESTS)' ./...
	go test -tags integration -count=1 -p 1 -v -run '$(TIMED_TESTS)' $(TIMED_PKGS)

$(FRONTEND_DIST): $(NODE_STAMP)
	$(NPM) run build

# e2e: 配布物を実際に起動する。GUI セッション上で実行すること（ヘッドレス環境では起動しない）。
#
# dist に依存させてある。実機での総合的な検証（起動時間・universal binary・
# 利用者領域への配置と起動・自動更新の一連）は **配布用の書庫そのもの**を対象にするため、
# build だけでは足りない。dist は build-universal を含むので build 単体より遅い。
test-e2e: dist
	go test -tags e2e -count=1 -timeout 600s ./e2e/...

# 共有フォルダ（SMB / NAS）上の同期先の実機確認。
# **検証ゲートには入れない**（対象の共有フォルダが要るため）。実行例:
#   REQWEAVE_SHARED_DIR=/Volumes/share/reqweave-test make -C app test-sharedfs
# 未指定だと fail する（前提が揃わないときに skip しない）。
test-sharedfs:
	go test -tags sharedfs -count=1 -timeout 900s -v ./internal/sync/...

# ---- 補助 ----------------------------------------------------------------

dev:
	$(WAILS) dev $(WAILS_TAGS)

# **利用者の実データへ触れずにアプリを起動する**。
#
# 手で確認するために普通に起動すると、`~/Library/Application Support/net.howashoji.reqweave`
# の**実データ（利用者 ID・表示名・キー参照名）を読み書きする**。初回起動の見え方を確かめたい、
# 画面を人に見せたい、といった場面で実データが画面に出る（2026-09-04 に実際に起きた）。
#
# 本ターゲットは $$HOME を毎回新しい一時フォルダへ向けて起動する（e2e と同じ隔離の仕方 =
# app/e2e/helper_test.go の launchOptions.home）。アプリ設定・プロジェクト・動作ログは
# すべてそこへ作られ、実データは読まれも書かれもしない。
#
#   make -C app run-clean                                        # build/bin のアプリ
#   make -C app run-clean RUN_CLEAN_APP=/path/to/ReqWeave.app    # 配布物から取り出した .app
#
# **キーチェーンだけは実環境と共有する**。macOS の Security フレームワークは
# ログインキーチェーンの場所を $$HOME から解決するため、隔離したままだと
# 「既定のキーチェーンが無い」状態になり（実測: `security default-keychain` が
# `A default keychain could not be found.`）、**シークレットキーを登録できず初期設定を完了できない**。
# OS の「キーチェーンが見つかりません／デフォルトに戻す」ダイアログまで出て、
# 押すと利用者のキーチェーン構成を変えうる。
# そこで **Keychains ディレクトリだけ実環境へのリンクを張る**。
# この結果、隔離起動で登録したキーは**実環境のキーチェーンに残る**。不要になったら
# アプリの設定画面から削除するか、`security delete-generic-password -s howashoji.reqweave -a '<参照名>'`。
RUN_CLEAN_APP ?= build/bin/ReqWeave.app
run-clean:
	@test -d "$(RUN_CLEAN_APP)" || { echo "✘ アプリがありません: $(RUN_CLEAN_APP)（make -C app build を先に実行するか RUN_CLEAN_APP= で指定）"; exit 1; }
	@test -d "$$HOME/Library/Keychains" || { echo "✘ キーチェーンの場所が見つかりません: $$HOME/Library/Keychains"; exit 1; }
	@home=$$(mktemp -d /tmp/reqweave-clean.XXXXXX); \
		mkdir -p $$home/Library; \
		ln -s "$$HOME/Library/Keychains" "$$home/Library/Keychains"; \
		echo "隔離した HOME: $$home"; \
		echo "アプリ設定・プロジェクト・動作ログは隔離先に作られます"; \
		echo "実データ（~/Library/Application Support/net.howashoji.reqweave）には触れません"; \
		echo "※ キーチェーンだけは実環境と共有します。登録したキーは実環境に残ります"; \
		HOME=$$home "$(RUN_CLEAN_APP)/Contents/MacOS/ReqWeave"; \
		echo "終了しました。この起動が作ったものは $$home 配下にあります（不要なら rm -rf してください）"

generate:
	$(WAILS) generate module

# 配布物名などで版番号を使う箇所のための表示（正本は $(VERSION_FILE)）。
version:
	@echo $(APP_VERSION)

# **build/release は消さない**。公証済みの配布物は作り直しに数時間かかるため、
# 掃除の巻き添えで消さない。捨てるときは明示的に rm する。
clean:
	rm -rf build/bin build/dist $(FRONTEND)/dist .gates
