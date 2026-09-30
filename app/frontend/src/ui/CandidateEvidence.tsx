import type {ReactNode} from 'react'
import './CandidateEvidence.css'

/*
 * 候補の根拠が特定できないときの示し方。
 *
 * 対話・資料取込・回答取込の 3 つの候補パネルが同じ規則で示すため、ここに 1 つだけ置く
 * （画面ごとに書くと、1 画面だけ「参照欠落」の語が残る、承認を選べてしまう、が再発する）。
 *
 * - 「参照欠落」の語だけで示さない。何が起きていて、次に何をするかを文で言う。
 * - 決定事項・未決事項・質問観点は根拠が無いと記録できない（保存の段階で拒否される）。
 *   承認を選べなくし、理由を示す（押してから失敗させない）。
 * - 要件項目は根拠なしでも記録でき、一覧で「参照欠落」と示される。
 */

/** 根拠の種類（画面ごとの言い方）。対話 = 発言 / 資料取込 = 資料の該当箇所 / 回答取込 = 回答 */
export type EvidenceSource = '発言' | '資料の該当箇所' | '回答'

type WithEvidence = {evidence_refs?: string[] | null; missing_evidence?: boolean}

/** 根拠が無い（または実在検証で除かれた）候補か */
export function lacksEvidence(c: WithEvidence): boolean {
    return Boolean(c.missing_evidence) || !(c.evidence_refs ?? []).length
}

/** 根拠が無いため承認を選べない理由（承認ボタンの無効化理由に使う） */
export function noEvidenceReason(source: EvidenceSource): string {
    return `根拠にした${source}を特定できないため、この候補は記録できません。破棄してください。`
}

/**
 * 根拠が無い候補の説明。根拠があるときは `children`（根拠の表示）をそのまま出す。
 *
 * `recordable` は根拠なしでも記録できる種別か（要件項目のみ true）。
 */
export function CandidateEvidence({
    candidate,
    source,
    recordable,
    children,
}: {
    candidate: WithEvidence
    source: EvidenceSource
    recordable: boolean
    children: ReactNode
}) {
    if (!lacksEvidence(candidate)) {
        return <>{children}</>
    }
    return (
        <p className="rw-candidate-evidence" role="note">
            根拠にした{source}を特定できませんでした。
            {recordable
                ? '承認はできますが、根拠へたどれない要件項目（一覧では「参照欠落」と表示）として残ります。'
                : 'このままでは記録できないため、この候補は破棄してください。'}
        </p>
    )
}
