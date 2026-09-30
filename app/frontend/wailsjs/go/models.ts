export namespace applog {
	
	export class DiagnosticItem {
	    name: string;
	    bytes: number;
	
	    static createFrom(source: any = {}) {
	        return new DiagnosticItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.bytes = source["bytes"];
	    }
	}
	export class Diagnostic {
	    environment: string;
	    items: DiagnosticItem[];
	    logText: string;
	    truncated: boolean;
	    totalBytes: number;
	
	    static createFrom(source: any = {}) {
	        return new Diagnostic(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.environment = source["environment"];
	        this.items = this.convertValues(source["items"], DiagnosticItem);
	        this.logText = source["logText"];
	        this.truncated = source["truncated"];
	        this.totalBytes = source["totalBytes"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class Logger {
	
	
	    static createFrom(source: any = {}) {
	        return new Logger(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}

}

export namespace binding {
	
	export class AITimeoutsView {
	    connectSeconds: number;
	    responseSeconds: number;
	    minConnectSeconds: number;
	    maxConnectSeconds: number;
	    minResponseSeconds: number;
	    maxResponseSeconds: number;
	
	    static createFrom(source: any = {}) {
	        return new AITimeoutsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connectSeconds = source["connectSeconds"];
	        this.responseSeconds = source["responseSeconds"];
	        this.minConnectSeconds = source["minConnectSeconds"];
	        this.maxConnectSeconds = source["maxConnectSeconds"];
	        this.minResponseSeconds = source["minResponseSeconds"];
	        this.maxResponseSeconds = source["maxResponseSeconds"];
	    }
	}
	export class AccountView {
	    email?: string;
	    planLabel: string;
	    accountLabel: string;
	    trainingNotice?: string;
	
	    static createFrom(source: any = {}) {
	        return new AccountView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.email = source["email"];
	        this.planLabel = source["planLabel"];
	        this.accountLabel = source["accountLabel"];
	        this.trainingNotice = source["trainingNotice"];
	    }
	}
	export class AnswerEvidenceView {
	    questionnaireId: string;
	    questionId: string;
	    questionText: string;
	    background?: string;
	    sourceIssue?: string;
	    respondent: string;
	    answeredAt: string;
	    kind: string;
	    selected?: string[];
	    freeText?: string;
	    body?: string;
	
	    static createFrom(source: any = {}) {
	        return new AnswerEvidenceView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.questionnaireId = source["questionnaireId"];
	        this.questionId = source["questionId"];
	        this.questionText = source["questionText"];
	        this.background = source["background"];
	        this.sourceIssue = source["sourceIssue"];
	        this.respondent = source["respondent"];
	        this.answeredAt = source["answeredAt"];
	        this.kind = source["kind"];
	        this.selected = source["selected"];
	        this.freeText = source["freeText"];
	        this.body = source["body"];
	    }
	}
	export class AnswerInput {
	    questionId: string;
	    kind: string;
	    selected?: string[];
	    freeText?: string;
	    body?: string;
	
	    static createFrom(source: any = {}) {
	        return new AnswerInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.questionId = source["questionId"];
	        this.kind = source["kind"];
	        this.selected = source["selected"];
	        this.freeText = source["freeText"];
	        this.body = source["body"];
	    }
	}
	export class AnswerMatchView {
	    questionId: string;
	    questionText: string;
	    sourceIssue: string;
	    kind?: string;
	    selected?: string[];
	    freeText?: string;
	    body?: string;
	    answered: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AnswerMatchView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.questionId = source["questionId"];
	        this.questionText = source["questionText"];
	        this.sourceIssue = source["sourceIssue"];
	        this.kind = source["kind"];
	        this.selected = source["selected"];
	        this.freeText = source["freeText"];
	        this.body = source["body"];
	        this.answered = source["answered"];
	    }
	}
	export class AppInfo {
	    name: string;
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new AppInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.version = source["version"];
	    }
	}
	export class ApprovalApplied {
	    decisionIds?: string[];
	    openIssueIds?: string[];
	    requirementIds?: string[];
	    termNames?: string[];
	    perspectiveIds?: string[];
	    resolvedIssueIds?: string[];
	    unblockedRequirementIds?: string[];
	    revertedRequirementIds?: string[];
	    notedIssueIds?: string[];
	    ownerUpdatedIssueIds?: string[];
	    questionnaireStatus?: string;
	    state?: string;
	
	    static createFrom(source: any = {}) {
	        return new ApprovalApplied(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.decisionIds = source["decisionIds"];
	        this.openIssueIds = source["openIssueIds"];
	        this.requirementIds = source["requirementIds"];
	        this.termNames = source["termNames"];
	        this.perspectiveIds = source["perspectiveIds"];
	        this.resolvedIssueIds = source["resolvedIssueIds"];
	        this.unblockedRequirementIds = source["unblockedRequirementIds"];
	        this.revertedRequirementIds = source["revertedRequirementIds"];
	        this.notedIssueIds = source["notedIssueIds"];
	        this.ownerUpdatedIssueIds = source["ownerUpdatedIssueIds"];
	        this.questionnaireStatus = source["questionnaireStatus"];
	        this.state = source["state"];
	    }
	}
	export class ConflictView {
	    id: string;
	    label: string;
	    baselineBody: string;
	    currentBody: string;
	    currentHash: string;
	
	    static createFrom(source: any = {}) {
	        return new ConflictView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.baselineBody = source["baselineBody"];
	        this.currentBody = source["currentBody"];
	        this.currentHash = source["currentHash"];
	    }
	}
	export class ApprovalOutcome {
	    applied?: ApprovalApplied;
	    conflicts?: ConflictView[];
	    notice?: string;
	
	    static createFrom(source: any = {}) {
	        return new ApprovalOutcome(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.applied = this.convertValues(source["applied"], ApprovalApplied);
	        this.conflicts = this.convertValues(source["conflicts"], ConflictView);
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AuthMethodOption {
	    id: string;
	    label: string;
	    description: string;
	    default: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AuthMethodOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.description = source["description"];
	        this.default = source["default"];
	    }
	}
	export class BackupGenerationView {
	    path: string;
	    createdAt: string;
	    sizeBytes: number;
	
	    static createFrom(source: any = {}) {
	        return new BackupGenerationView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.createdAt = source["createdAt"];
	        this.sizeBytes = source["sizeBytes"];
	    }
	}
	export class BackupPreview {
	    sourcePath: string;
	    projectId: string;
	    targetSystemName: string;
	    duplicatePath?: string;
	    notice?: string;
	
	    static createFrom(source: any = {}) {
	        return new BackupPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourcePath = source["sourcePath"];
	        this.projectId = source["projectId"];
	        this.targetSystemName = source["targetSystemName"];
	        this.duplicatePath = source["duplicatePath"];
	        this.notice = source["notice"];
	    }
	}
	export class ChangeHistoryView {
	    at: string;
	    author: string;
	    target: string;
	    change: string;
	    before?: string;
	    after?: string;
	
	    static createFrom(source: any = {}) {
	        return new ChangeHistoryView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.at = source["at"];
	        this.author = source["author"];
	        this.target = source["target"];
	        this.change = source["change"];
	        this.before = source["before"];
	        this.after = source["after"];
	    }
	}
	export class ChangeSummaryItemView {
	    kind: string;
	    kindLabel: string;
	    target: string;
	    summary: string;
	    author: string;
	    at: string;
	
	    static createFrom(source: any = {}) {
	        return new ChangeSummaryItemView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.kindLabel = source["kindLabel"];
	        this.target = source["target"];
	        this.summary = source["summary"];
	        this.author = source["author"];
	        this.at = source["at"];
	    }
	}
	export class ChangeSummaryView {
	    incorporation?: string;
	    noIncorporation: boolean;
	    counts?: Record<string, number>;
	    items?: ChangeSummaryItemView[];
	    notice?: string;
	
	    static createFrom(source: any = {}) {
	        return new ChangeSummaryView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.incorporation = source["incorporation"];
	        this.noIncorporation = source["noIncorporation"];
	        this.counts = source["counts"];
	        this.items = this.convertValues(source["items"], ChangeSummaryItemView);
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CloneSyncProjectRequest {
	    kind: string;
	    location: string;
	    dest: string;
	    credentialKind?: string;
	    username?: string;
	    secret?: string;
	    externalConsent: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CloneSyncProjectRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.location = source["location"];
	        this.dest = source["dest"];
	        this.credentialKind = source["credentialKind"];
	        this.username = source["username"];
	        this.secret = source["secret"];
	        this.externalConsent = source["externalConsent"];
	    }
	}
	export class SyncFailureView {
	    kindLabel: string;
	    message: string;
	    detail?: string;
	
	    static createFrom(source: any = {}) {
	        return new SyncFailureView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kindLabel = source["kindLabel"];
	        this.message = source["message"];
	        this.detail = source["detail"];
	    }
	}
	export class ProjectSummary {
	    path: string;
	    projectId: string;
	    targetSystemName: string;
	    phase: string;
	    phaseLabel: string;
	    updatedAt: string;
	    role: string;
	    roleLabel: string;
	    working?: string[];
	    available: boolean;
	    syncConfigured: boolean;
	    syncKindLabel?: string;
	    lastSyncedAt?: string;
	    hasUnpublished: boolean;
	    notice?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProjectSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.projectId = source["projectId"];
	        this.targetSystemName = source["targetSystemName"];
	        this.phase = source["phase"];
	        this.phaseLabel = source["phaseLabel"];
	        this.updatedAt = source["updatedAt"];
	        this.role = source["role"];
	        this.roleLabel = source["roleLabel"];
	        this.working = source["working"];
	        this.available = source["available"];
	        this.syncConfigured = source["syncConfigured"];
	        this.syncKindLabel = source["syncKindLabel"];
	        this.lastSyncedAt = source["lastSyncedAt"];
	        this.hasUnpublished = source["hasUnpublished"];
	        this.notice = source["notice"];
	    }
	}
	export class CloneSyncProjectView {
	    done: boolean;
	    project?: ProjectSummary;
	    failure?: SyncFailureView;
	    notice: string;
	
	    static createFrom(source: any = {}) {
	        return new CloneSyncProjectView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.done = source["done"];
	        this.project = this.convertValues(source["project"], ProjectSummary);
	        this.failure = this.convertValues(source["failure"], SyncFailureView);
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ConfirmCheckView {
	    confirmable: boolean;
	    draftRequirements?: string[];
	    blockingIssues?: string[];
	    noRequirements?: boolean;
	    hasDraft: boolean;
	    reason?: string;
	
	    static createFrom(source: any = {}) {
	        return new ConfirmCheckView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.confirmable = source["confirmable"];
	        this.draftRequirements = source["draftRequirements"];
	        this.blockingIssues = source["blockingIssues"];
	        this.noRequirements = source["noRequirements"];
	        this.hasDraft = source["hasDraft"];
	        this.reason = source["reason"];
	    }
	}
	export class ConfirmResultView {
	    version: number;
	    agreedRequirements?: string[];
	    canMoveToBasicDesign: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ConfirmResultView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.agreedRequirements = source["agreedRequirements"];
	        this.canMoveToBasicDesign = source["canMoveToBasicDesign"];
	    }
	}
	
	export class CreateProjectRequest {
	    path: string;
	    targetSystemName: string;
	    summary: string;
	    domainPresets: string[];
	
	    static createFrom(source: any = {}) {
	        return new CreateProjectRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.targetSystemName = source["targetSystemName"];
	        this.summary = source["summary"];
	        this.domainPresets = source["domainPresets"];
	    }
	}
	export class CreateSyncMergeOpenIssueRequest {
	    conflictId: string;
	    label: string;
	    theirsAuthor: string;
	    theirs: string;
	    ours: string;
	    owner?: string;
	    due?: string;
	
	    static createFrom(source: any = {}) {
	        return new CreateSyncMergeOpenIssueRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.conflictId = source["conflictId"];
	        this.label = source["label"];
	        this.theirsAuthor = source["theirsAuthor"];
	        this.theirs = source["theirs"];
	        this.ours = source["ours"];
	        this.owner = source["owner"];
	        this.due = source["due"];
	    }
	}
	export class DecisionView {
	    id: string;
	    topicKey: string;
	    decidedAt: string;
	    body: string;
	    evidence: string[];
	    supersededBy?: string;
	    supersedes?: string;
	
	    static createFrom(source: any = {}) {
	        return new DecisionView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.topicKey = source["topicKey"];
	        this.decidedAt = source["decidedAt"];
	        this.body = source["body"];
	        this.evidence = source["evidence"];
	        this.supersededBy = source["supersededBy"];
	        this.supersedes = source["supersedes"];
	    }
	}
	export class DeletePreview {
	    path: string;
	    targetSystemName: string;
	    hasDocuments: boolean;
	    backupCount: number;
	    deletable: boolean;
	    notice?: string;
	
	    static createFrom(source: any = {}) {
	        return new DeletePreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.targetSystemName = source["targetSystemName"];
	        this.hasDocuments = source["hasDocuments"];
	        this.backupCount = source["backupCount"];
	        this.deletable = source["deletable"];
	        this.notice = source["notice"];
	    }
	}
	export class DialogueCompletenessView {
	    chapters: dialogue.ChapterCompleteness[];
	    confirmation: dialogue.Confirmation;
	
	    static createFrom(source: any = {}) {
	        return new DialogueCompletenessView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.chapters = this.convertValues(source["chapters"], dialogue.ChapterCompleteness);
	        this.confirmation = this.convertValues(source["confirmation"], dialogue.Confirmation);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DialogueSessionView {
	    id: string;
	    type: string;
	    phase: string;
	    startedAt: string;
	    author?: string;
	
	    static createFrom(source: any = {}) {
	        return new DialogueSessionView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.type = source["type"];
	        this.phase = source["phase"];
	        this.startedAt = source["startedAt"];
	        this.author = source["author"];
	    }
	}
	export class DialogueOpenResult {
	    projectPath: string;
	    projectId: string;
	    phase: string;
	    targetName: string;
	    sessions: DialogueSessionView[];
	
	    static createFrom(source: any = {}) {
	        return new DialogueOpenResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectPath = source["projectPath"];
	        this.projectId = source["projectId"];
	        this.phase = source["phase"];
	        this.targetName = source["targetName"];
	        this.sessions = this.convertValues(source["sessions"], DialogueSessionView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class DocumentChapterView {
	    fileName: string;
	    chapter: string;
	    title: string;
	    body: string;
	    covers?: string[];
	
	    static createFrom(source: any = {}) {
	        return new DocumentChapterView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fileName = source["fileName"];
	        this.chapter = source["chapter"];
	        this.title = source["title"];
	        this.body = source["body"];
	        this.covers = source["covers"];
	    }
	}
	export class DocumentVersionView {
	    version: number;
	    label: string;
	    confirmedAt?: string;
	    sourceCount?: number;
	    hasContent: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DocumentVersionView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.label = source["label"];
	        this.confirmedAt = source["confirmedAt"];
	        this.sourceCount = source["sourceCount"];
	        this.hasContent = source["hasContent"];
	    }
	}
	export class DomainPresetOption {
	    id: string;
	    label: string;
	    summary: string;
	
	    static createFrom(source: any = {}) {
	        return new DomainPresetOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.summary = source["summary"];
	    }
	}
	export class EditRequirementRequest {
	    id: string;
	    title: string;
	    body: string;
	    acceptanceCriteria?: string[];
	    priority?: string;
	
	    static createFrom(source: any = {}) {
	        return new EditRequirementRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.body = source["body"];
	        this.acceptanceCriteria = source["acceptanceCriteria"];
	        this.priority = source["priority"];
	    }
	}
	export class EffortOption {
	    id: string;
	    label: string;
	    description: string;
	    default: boolean;
	
	    static createFrom(source: any = {}) {
	        return new EffortOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.description = source["description"];
	        this.default = source["default"];
	    }
	}
	export class UtteranceView {
	    id: string;
	    speaker: string;
	    at: string;
	    status: string;
	    body: string;
	
	    static createFrom(source: any = {}) {
	        return new UtteranceView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.speaker = source["speaker"];
	        this.at = source["at"];
	        this.status = source["status"];
	        this.body = source["body"];
	    }
	}
	export class EvidenceView {
	    ref: string;
	    found: boolean;
	    context?: UtteranceView[];
	    import?: importer.RefLocation;
	    answer?: AnswerEvidenceView;
	
	    static createFrom(source: any = {}) {
	        return new EvidenceView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ref = source["ref"];
	        this.found = source["found"];
	        this.context = this.convertValues(source["context"], UtteranceView);
	        this.import = this.convertValues(source["import"], importer.RefLocation);
	        this.answer = this.convertValues(source["answer"], AnswerEvidenceView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ExportRequestView {
	    destination: string;
	    requirements: number;
	    basicDesign: number;
	    includeBasicDesign: boolean;
	    acceptWarnings: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ExportRequestView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.destination = source["destination"];
	        this.requirements = source["requirements"];
	        this.basicDesign = source["basicDesign"];
	        this.includeBasicDesign = source["includeBasicDesign"];
	        this.acceptWarnings = source["acceptWarnings"];
	    }
	}
	export class FeedbackClassificationOption {
	    value: string;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new FeedbackClassificationOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.value = source["value"];
	        this.label = source["label"];
	    }
	}
	export class GuideView {
	    stageId: string;
	    stageLabel: string;
	    stageIndex: number;
	    stageTotal: number;
	    next: string;
	    target: string;
	    button: string;
	    note?: string;
	    noteTarget?: string;
	    noteButton?: string;
	    stages: guide.Stage[];
	
	    static createFrom(source: any = {}) {
	        return new GuideView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stageId = source["stageId"];
	        this.stageLabel = source["stageLabel"];
	        this.stageIndex = source["stageIndex"];
	        this.stageTotal = source["stageTotal"];
	        this.next = source["next"];
	        this.target = source["target"];
	        this.button = source["button"];
	        this.note = source["note"];
	        this.noteTarget = source["noteTarget"];
	        this.noteButton = source["noteButton"];
	        this.stages = this.convertValues(source["stages"], guide.Stage);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class IDRangeWarningView {
	    kindLabel: string;
	    remaining: number;
	    exhausted: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new IDRangeWarningView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kindLabel = source["kindLabel"];
	        this.remaining = source["remaining"];
	        this.exhausted = source["exhausted"];
	        this.message = source["message"];
	    }
	}
	export class ImpactChapterView {
	    kind: string;
	    fileName: string;
	    title: string;
	
	    static createFrom(source: any = {}) {
	        return new ImpactChapterView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.fileName = source["fileName"];
	        this.title = source["title"];
	    }
	}
	export class ImpactView {
	    target: string;
	    chapters?: ImpactChapterView[];
	    requirements?: string[];
	    openIssues?: string[];
	    designChapters?: ImpactChapterView[];
	
	    static createFrom(source: any = {}) {
	        return new ImpactView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.target = source["target"];
	        this.chapters = this.convertValues(source["chapters"], ImpactChapterView);
	        this.requirements = source["requirements"];
	        this.openIssues = source["openIssues"];
	        this.designChapters = this.convertValues(source["designChapters"], ImpactChapterView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ImportSkip {
	    name: string;
	    reason: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportSkip(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.reason = source["reason"];
	    }
	}
	export class ImportView {
	    id: string;
	    kind: string;
	    kindLabel: string;
	    sourceName: string;
	    importedAt: string;
	    extracted: boolean;
	    statusLabel: string;
	    classification?: string;
	    classificationLabel?: string;
	    fromTemplate?: boolean;
	    analysisState?: string;
	    analysisLabel?: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.kindLabel = source["kindLabel"];
	        this.sourceName = source["sourceName"];
	        this.importedAt = source["importedAt"];
	        this.extracted = source["extracted"];
	        this.statusLabel = source["statusLabel"];
	        this.classification = source["classification"];
	        this.classificationLabel = source["classificationLabel"];
	        this.fromTemplate = source["fromTemplate"];
	        this.analysisState = source["analysisState"];
	        this.analysisLabel = source["analysisLabel"];
	    }
	}
	export class ImportBatchView {
	    imported: ImportView[];
	    importedCount: number;
	    failed: ImportSkip[];
	    failedCount: number;
	    skippedCount: number;
	    notice: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportBatchView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.imported = this.convertValues(source["imported"], ImportView);
	        this.importedCount = source["importedCount"];
	        this.failed = this.convertValues(source["failed"], ImportSkip);
	        this.failedCount = source["failedCount"];
	        this.skippedCount = source["skippedCount"];
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ImportContentView {
	    id: string;
	    extracted: string;
	    extractionFailed: boolean;
	    sourceText?: string;
	    sourcePath: string;
	    sourceName: string;
	    notice?: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportContentView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.extracted = source["extracted"];
	        this.extractionFailed = source["extractionFailed"];
	        this.sourceText = source["sourceText"];
	        this.sourcePath = source["sourcePath"];
	        this.sourceName = source["sourceName"];
	        this.notice = source["notice"];
	    }
	}
	export class ImportKindOption {
	    id: string;
	    label: string;
	    summary: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportKindOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.summary = source["summary"];
	    }
	}
	export class ImportResultView {
	    questionnaireId: string;
	    status: string;
	    statusLabel: string;
	    sessionIds?: string[];
	
	    static createFrom(source: any = {}) {
	        return new ImportResultView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.questionnaireId = source["questionnaireId"];
	        this.status = source["status"];
	        this.statusLabel = source["statusLabel"];
	        this.sessionIds = source["sessionIds"];
	    }
	}
	export class ImportWarningView {
	    kind: string;
	    message: string;
	    before?: string;
	    after?: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportWarningView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.message = source["message"];
	        this.before = source["before"];
	        this.after = source["after"];
	    }
	}
	export class ImportReviewView {
	    questionnaireId: string;
	    addressee: string;
	    respondent: string;
	    // Go type: time
	    answeredAt: any;
	    matches: AnswerMatchView[];
	    missingQuestionIds?: string[];
	    unknownQuestionIds?: string[];
	    warnings?: ImportWarningView[];
	    sessionCount: number;
	
	    static createFrom(source: any = {}) {
	        return new ImportReviewView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.questionnaireId = source["questionnaireId"];
	        this.addressee = source["addressee"];
	        this.respondent = source["respondent"];
	        this.answeredAt = this.convertValues(source["answeredAt"], null);
	        this.matches = this.convertValues(source["matches"], AnswerMatchView);
	        this.missingQuestionIds = source["missingQuestionIds"];
	        this.unknownQuestionIds = source["unknownQuestionIds"];
	        this.warnings = this.convertValues(source["warnings"], ImportWarningView);
	        this.sessionCount = source["sessionCount"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ImportScanEntry {
	    name: string;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new ImportScanEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.size = source["size"];
	    }
	}
	export class ImportScanView {
	    dir: string;
	    count: number;
	    totalBytes: number;
	    totalSizeLabel: string;
	    files: ImportScanEntry[];
	    skipped: ImportSkip[];
	    skippedCount: number;
	    blocked?: string;
	    notice?: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportScanView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dir = source["dir"];
	        this.count = source["count"];
	        this.totalBytes = source["totalBytes"];
	        this.totalSizeLabel = source["totalSizeLabel"];
	        this.files = this.convertValues(source["files"], ImportScanEntry);
	        this.skipped = this.convertValues(source["skipped"], ImportSkip);
	        this.skippedCount = source["skippedCount"];
	        this.blocked = source["blocked"];
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	export class QuestionInput {
	    sourceIssue: string;
	    text: string;
	    background: string;
	    answerFormat: string;
	    choices?: string[];
	    terms?: string[];
	
	    static createFrom(source: any = {}) {
	        return new QuestionInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceIssue = source["sourceIssue"];
	        this.text = source["text"];
	        this.background = source["background"];
	        this.answerFormat = source["answerFormat"];
	        this.choices = source["choices"];
	        this.terms = source["terms"];
	    }
	}
	export class IssueDraftRequest {
	    addresseeRef: string;
	    questions: QuestionInput[];
	
	    static createFrom(source: any = {}) {
	        return new IssueDraftRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.addresseeRef = source["addresseeRef"];
	        this.questions = this.convertValues(source["questions"], QuestionInput);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TermView {
	    name: string;
	    nameEn: string;
	    definition: string;
	
	    static createFrom(source: any = {}) {
	        return new TermView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.nameEn = source["nameEn"];
	        this.definition = source["definition"];
	    }
	}
	export class IssuePreviewView {
	    questionnaireId: string;
	    addressee: string;
	    questionCount: number;
	    sourceIssues: string[];
	    questionnaireMarkdown: string;
	    terms?: TermView[];
	    excluded: string[];
	
	    static createFrom(source: any = {}) {
	        return new IssuePreviewView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.questionnaireId = source["questionnaireId"];
	        this.addressee = source["addressee"];
	        this.questionCount = source["questionCount"];
	        this.sourceIssues = source["sourceIssues"];
	        this.questionnaireMarkdown = source["questionnaireMarkdown"];
	        this.terms = this.convertValues(source["terms"], TermView);
	        this.excluded = source["excluded"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class IssueResultView {
	    questionnaireId: string;
	    path: string;
	    // Go type: time
	    issuedAt: any;
	    passcode: string;
	    passcodeNotice: string;
	    reissued: boolean;
	
	    static createFrom(source: any = {}) {
	        return new IssueResultView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.questionnaireId = source["questionnaireId"];
	        this.path = source["path"];
	        this.issuedAt = this.convertValues(source["issuedAt"], null);
	        this.passcode = source["passcode"];
	        this.passcodeNotice = source["passcodeNotice"];
	        this.reissued = source["reissued"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class MemberRequest {
	    authorId: string;
	    displayName: string;
	    role: string;
	
	    static createFrom(source: any = {}) {
	        return new MemberRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.authorId = source["authorId"];
	        this.displayName = source["displayName"];
	        this.role = source["role"];
	    }
	}
	export class MemberView {
	    authorId: string;
	    displayName: string;
	    role: string;
	    roleLabel: string;
	    addedAt: string;
	    addedBy: string;
	    self: boolean;
	
	    static createFrom(source: any = {}) {
	        return new MemberView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.authorId = source["authorId"];
	        this.displayName = source["displayName"];
	        this.role = source["role"];
	        this.roleLabel = source["roleLabel"];
	        this.addedAt = source["addedAt"];
	        this.addedBy = source["addedBy"];
	        this.self = source["self"];
	    }
	}
	export class MigratableProject {
	    path: string;
	    targetSystemName: string;
	    newName: string;
	
	    static createFrom(source: any = {}) {
	        return new MigratableProject(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.targetSystemName = source["targetSystemName"];
	        this.newName = source["newName"];
	    }
	}
	export class ModelOption {
	    id: string;
	    displayName: string;
	    contextWindow: number;
	    maxOutput: number;
	    tier: string;
	    recommended: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ModelOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.displayName = source["displayName"];
	        this.contextWindow = source["contextWindow"];
	        this.maxOutput = source["maxOutput"];
	        this.tier = source["tier"];
	        this.recommended = source["recommended"];
	    }
	}
	export class ModelList {
	    models: ModelOption[];
	    fromKnownList: boolean;
	    notice?: string;
	
	    static createFrom(source: any = {}) {
	        return new ModelList(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.models = this.convertValues(source["models"], ModelOption);
	        this.fromKnownList = source["fromKnownList"];
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class OpenFileEvent {
	    kind: string;
	    filePath?: string;
	    fileName?: string;
	    message?: string;
	
	    static createFrom(source: any = {}) {
	        return new OpenFileEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.filePath = source["filePath"];
	        this.fileName = source["fileName"];
	        this.message = source["message"];
	    }
	}
	export class OpenIssueView {
	    id: string;
	    topic: string;
	    owner: string;
	    due?: string;
	    status: string;
	    overdue: boolean;
	    blocking?: string[];
	    questionnaireStatus: string;
	    resolvedBy?: string;
	    evidence: string[];
	
	    static createFrom(source: any = {}) {
	        return new OpenIssueView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.topic = source["topic"];
	        this.owner = source["owner"];
	        this.due = source["due"];
	        this.status = source["status"];
	        this.overdue = source["overdue"];
	        this.blocking = source["blocking"];
	        this.questionnaireStatus = source["questionnaireStatus"];
	        this.resolvedBy = source["resolvedBy"];
	        this.evidence = source["evidence"];
	    }
	}
	export class PaneWidthsView {
	    versions: number;
	    checks: number;
	    min: number;
	    max: number;
	
	    static createFrom(source: any = {}) {
	        return new PaneWidthsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.versions = source["versions"];
	        this.checks = source["checks"];
	        this.min = source["min"];
	        this.max = source["max"];
	    }
	}
	export class PermissionView {
	    role: string;
	    roleLabel: string;
	    canEdit: boolean;
	    reason?: string;
	    canManageMembers: boolean;
	    manageReason?: string;
	    canManageUsageLimit: boolean;
	    usageLimitReason?: string;
	
	    static createFrom(source: any = {}) {
	        return new PermissionView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.roleLabel = source["roleLabel"];
	        this.canEdit = source["canEdit"];
	        this.reason = source["reason"];
	        this.canManageMembers = source["canManageMembers"];
	        this.manageReason = source["manageReason"];
	        this.canManageUsageLimit = source["canManageUsageLimit"];
	        this.usageLimitReason = source["usageLimitReason"];
	    }
	}
	export class PerspectiveRequest {
	    id: string;
	    name: string;
	    summary: string;
	
	    static createFrom(source: any = {}) {
	        return new PerspectiveRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.summary = source["summary"];
	    }
	}
	export class PerspectiveView {
	    id: string;
	    name: string;
	    summary: string;
	    origin: string;
	    originLabel: string;
	    evidence?: string;
	    topicKey: string;
	    // Go type: time
	    createdAt: any;
	    author: string;
	
	    static createFrom(source: any = {}) {
	        return new PerspectiveView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.summary = source["summary"];
	        this.origin = source["origin"];
	        this.originLabel = source["originLabel"];
	        this.evidence = source["evidence"];
	        this.topicKey = source["topicKey"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.author = source["author"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PlanUsageWindowView {
	    label: string;
	    usedPercent: number;
	    windowDurationMins: number;
	    windowLabel?: string;
	    resetsAt?: string;
	
	    static createFrom(source: any = {}) {
	        return new PlanUsageWindowView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.usedPercent = source["usedPercent"];
	        this.windowDurationMins = source["windowDurationMins"];
	        this.windowLabel = source["windowLabel"];
	        this.resetsAt = source["resetsAt"];
	    }
	}
	export class PlanUsageView {
	    available: boolean;
	    fetched: boolean;
	    planLabel?: string;
	    receivedAt?: string;
	    windows?: PlanUsageWindowView[];
	    notice?: string;
	
	    static createFrom(source: any = {}) {
	        return new PlanUsageView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.fetched = source["fetched"];
	        this.planLabel = source["planLabel"];
	        this.receivedAt = source["receivedAt"];
	        this.windows = this.convertValues(source["windows"], PlanUsageWindowView);
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class ProgressReportRequest {
	    from: string;
	    to: string;
	
	    static createFrom(source: any = {}) {
	        return new ProgressReportRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	    }
	}
	export class ProgressReportView {
	    markdown: string;
	    from: string;
	    to: string;
	
	    static createFrom(source: any = {}) {
	        return new ProgressReportView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.markdown = source["markdown"];
	        this.from = source["from"];
	        this.to = source["to"];
	    }
	}
	
	export class ProjectUsageRow {
	    path: string;
	    projectId: string;
	    targetSystemName: string;
	    aggregated: boolean;
	    notice?: string;
	    tokens: number;
	    missingRecords: number;
	    lastUsedAt?: string;
	    limitTokens?: number;
	    consumptionRatio?: number;
	
	    static createFrom(source: any = {}) {
	        return new ProjectUsageRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.projectId = source["projectId"];
	        this.targetSystemName = source["targetSystemName"];
	        this.aggregated = source["aggregated"];
	        this.notice = source["notice"];
	        this.tokens = source["tokens"];
	        this.missingRecords = source["missingRecords"];
	        this.lastUsedAt = source["lastUsedAt"];
	        this.limitTokens = source["limitTokens"];
	        this.consumptionRatio = source["consumptionRatio"];
	    }
	}
	export class ProviderConfigView {
	    label: string;
	    providerId: string;
	    providerLabel: string;
	    model: string;
	    effort: string;
	    effortLabel: string;
	    authMethod: string;
	    authMethodLabel: string;
	    keyState: string;
	    keyMasked: string;
	    isDefault: boolean;
	    policyUrl: string;
	    notice?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProviderConfigView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.providerId = source["providerId"];
	        this.providerLabel = source["providerLabel"];
	        this.model = source["model"];
	        this.effort = source["effort"];
	        this.effortLabel = source["effortLabel"];
	        this.authMethod = source["authMethod"];
	        this.authMethodLabel = source["authMethodLabel"];
	        this.keyState = source["keyState"];
	        this.keyMasked = source["keyMasked"];
	        this.isDefault = source["isDefault"];
	        this.policyUrl = source["policyUrl"];
	        this.notice = source["notice"];
	    }
	}
	export class ProviderOption {
	    id: string;
	    displayName: string;
	    policyUrl: string;
	    notice?: string;
	    authMethods?: AuthMethodOption[];
	
	    static createFrom(source: any = {}) {
	        return new ProviderOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.displayName = source["displayName"];
	        this.policyUrl = source["policyUrl"];
	        this.notice = source["notice"];
	        this.authMethods = this.convertValues(source["authMethods"], AuthMethodOption);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class QuestionDraftView {
	    sourceIssue: string;
	    text: string;
	    background: string;
	    answerFormat: string;
	    choices?: string[];
	
	    static createFrom(source: any = {}) {
	        return new QuestionDraftView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceIssue = source["sourceIssue"];
	        this.text = source["text"];
	        this.background = source["background"];
	        this.answerFormat = source["answerFormat"];
	        this.choices = source["choices"];
	    }
	}
	export class QuestionDraftsView {
	    drafts: QuestionDraftView[];
	    fallback: boolean;
	    notice?: string;
	
	    static createFrom(source: any = {}) {
	        return new QuestionDraftsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.drafts = this.convertValues(source["drafts"], QuestionDraftView);
	        this.fallback = source["fallback"];
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class QuestionnaireView {
	    id: string;
	    addressee: string;
	    // Go type: time
	    issuedAt: any;
	    status: string;
	    statusLabel: string;
	    elapsedDays: number;
	    sourceIssues?: string[];
	
	    static createFrom(source: any = {}) {
	        return new QuestionnaireView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.addressee = source["addressee"];
	        this.issuedAt = this.convertValues(source["issuedAt"], null);
	        this.status = source["status"];
	        this.statusLabel = source["statusLabel"];
	        this.elapsedDays = source["elapsedDays"];
	        this.sourceIssues = source["sourceIssues"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RegisterSyncCredentialRequest {
	    projectId: string;
	    kind: string;
	    username: string;
	    secret: string;
	
	    static createFrom(source: any = {}) {
	        return new RegisterSyncCredentialRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.kind = source["kind"];
	        this.username = source["username"];
	        this.secret = source["secret"];
	    }
	}
	export class RequirementView {
	    id: string;
	    title: string;
	    chapter: string;
	    kind: string;
	    priority: string;
	    status: string;
	    body: string;
	    acceptanceCriteria?: string[];
	    decisions?: string[];
	    evidence?: string[];
	    blockedBy?: string[];
	    revertedReason?: string;
	    missingEvidence: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RequirementView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.chapter = source["chapter"];
	        this.kind = source["kind"];
	        this.priority = source["priority"];
	        this.status = source["status"];
	        this.body = source["body"];
	        this.acceptanceCriteria = source["acceptanceCriteria"];
	        this.decisions = source["decisions"];
	        this.evidence = source["evidence"];
	        this.blockedBy = source["blockedBy"];
	        this.revertedReason = source["revertedReason"];
	        this.missingEvidence = source["missingEvidence"];
	    }
	}
	export class ReservationCheckView {
	    reserved: boolean;
	    holder?: string;
	    holderId?: string;
	    startedAt?: string;
	    warning?: string;
	
	    static createFrom(source: any = {}) {
	        return new ReservationCheckView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.reserved = source["reserved"];
	        this.holder = source["holder"];
	        this.holderId = source["holderId"];
	        this.startedAt = source["startedAt"];
	        this.warning = source["warning"];
	    }
	}
	export class ReservationView {
	    target: string;
	    targetLabel: string;
	    mode: string;
	    modeLabel: string;
	    author: string;
	    authorId: string;
	    startedAt: string;
	    self: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ReservationView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.target = source["target"];
	        this.targetLabel = source["targetLabel"];
	        this.mode = source["mode"];
	        this.modeLabel = source["modeLabel"];
	        this.author = source["author"];
	        this.authorId = source["authorId"];
	        this.startedAt = source["startedAt"];
	        this.self = source["self"];
	    }
	}
	export class ReservationsView {
	    items: ReservationView[];
	    notice: string;
	
	    static createFrom(source: any = {}) {
	        return new ReservationsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.items = this.convertValues(source["items"], ReservationView);
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RespondUtteranceView {
	    id: string;
	    speakerLabel: string;
	    isAgent: boolean;
	    at: string;
	    body: string;
	    interrupted?: boolean;
	    canApply: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RespondUtteranceView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.speakerLabel = source["speakerLabel"];
	        this.isAgent = source["isAgent"];
	        this.at = source["at"];
	        this.body = source["body"];
	        this.interrupted = source["interrupted"];
	        this.canApply = source["canApply"];
	    }
	}
	export class RespondAIDialogueView {
	    enabled: boolean;
	    providerId?: string;
	    providerLabel?: string;
	    authMethod?: string;
	    authMethodLabel?: string;
	    canSignOut: boolean;
	    running: boolean;
	    utterances: RespondUtteranceView[];
	
	    static createFrom(source: any = {}) {
	        return new RespondAIDialogueView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.providerId = source["providerId"];
	        this.providerLabel = source["providerLabel"];
	        this.authMethod = source["authMethod"];
	        this.authMethodLabel = source["authMethodLabel"];
	        this.canSignOut = source["canSignOut"];
	        this.running = source["running"];
	        this.utterances = this.convertValues(source["utterances"], RespondUtteranceView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RespondAIScopeView {
	    sent: string[];
	    notSent: string[];
	    notice?: string;
	    policyUrl?: string;
	    noRecordNotice: string;
	
	    static createFrom(source: any = {}) {
	        return new RespondAIScopeView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sent = source["sent"];
	        this.notSent = source["notSent"];
	        this.notice = source["notice"];
	        this.policyUrl = source["policyUrl"];
	        this.noRecordNotice = source["noRecordNotice"];
	    }
	}
	export class RespondAnswerDraftView {
	    questionId: string;
	    freeText: string;
	    notice: string;
	
	    static createFrom(source: any = {}) {
	        return new RespondAnswerDraftView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.questionId = source["questionId"];
	        this.freeText = source["freeText"];
	        this.notice = source["notice"];
	    }
	}
	export class RespondAnswerView {
	    questionId: string;
	    kind: string;
	    selected?: string[];
	    freeText?: string;
	    body?: string;
	
	    static createFrom(source: any = {}) {
	        return new RespondAnswerView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.questionId = source["questionId"];
	        this.kind = source["kind"];
	        this.selected = source["selected"];
	        this.freeText = source["freeText"];
	        this.body = source["body"];
	    }
	}
	export class RespondExportView {
	    path: string;
	    notice: string;
	
	    static createFrom(source: any = {}) {
	        return new RespondExportView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.notice = source["notice"];
	    }
	}
	export class RespondTermView {
	    name: string;
	    definition: string;
	
	    static createFrom(source: any = {}) {
	        return new RespondTermView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.definition = source["definition"];
	    }
	}
	export class RespondQuestionView {
	    id: string;
	    text: string;
	    background: string;
	    answerFormat: string;
	    choices?: string[];
	
	    static createFrom(source: any = {}) {
	        return new RespondQuestionView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.text = source["text"];
	        this.background = source["background"];
	        this.answerFormat = source["answerFormat"];
	        this.choices = source["choices"];
	    }
	}
	export class RespondOpenResult {
	    questionnaireId: string;
	    addressee: string;
	    questions: RespondQuestionView[];
	    answers?: RespondAnswerView[];
	    terms?: RespondTermView[];
	    status: string;
	    restored: boolean;
	    notice?: string;
	
	    static createFrom(source: any = {}) {
	        return new RespondOpenResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.questionnaireId = source["questionnaireId"];
	        this.addressee = source["addressee"];
	        this.questions = this.convertValues(source["questions"], RespondQuestionView);
	        this.answers = this.convertValues(source["answers"], RespondAnswerView);
	        this.terms = this.convertValues(source["terms"], RespondTermView);
	        this.status = source["status"];
	        this.restored = source["restored"];
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RespondProgressView {
	    answered: number;
	    total: number;
	    unansweredIds?: string[];
	
	    static createFrom(source: any = {}) {
	        return new RespondProgressView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.answered = source["answered"];
	        this.total = source["total"];
	        this.unansweredIds = source["unansweredIds"];
	    }
	}
	
	
	
	export class ScaleWarning {
	    kind: string;
	    label: string;
	    level: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new ScaleWarning(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.label = source["label"];
	        this.level = source["level"];
	        this.message = source["message"];
	    }
	}
	export class SetSyncRemoteRequest {
	    kind: string;
	    location: string;
	    externalConsent: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SetSyncRemoteRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.location = source["location"];
	        this.externalConsent = source["externalConsent"];
	    }
	}
	export class SettingsView {
	    authorId: string;
	    displayName: string;
	    providers: ProviderConfigView[];
	    options: ProviderOption[];
	    efforts: EffortOption[];
	    dataPolicyNotice: string;
	    aiReady: boolean;
	    aiBlockedReason?: string;
	    theme: string;
	    aiTimeouts: AITimeoutsView;
	
	    static createFrom(source: any = {}) {
	        return new SettingsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.authorId = source["authorId"];
	        this.displayName = source["displayName"];
	        this.providers = this.convertValues(source["providers"], ProviderConfigView);
	        this.options = this.convertValues(source["options"], ProviderOption);
	        this.efforts = this.convertValues(source["efforts"], EffortOption);
	        this.dataPolicyNotice = source["dataPolicyNotice"];
	        this.aiReady = source["aiReady"];
	        this.aiBlockedReason = source["aiBlockedReason"];
	        this.theme = source["theme"];
	        this.aiTimeouts = this.convertValues(source["aiTimeouts"], AITimeoutsView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SetupRequest {
	    providerId: string;
	    label: string;
	    model: string;
	    effort: string;
	    authMethod?: string;
	    authorId: string;
	    displayName: string;
	
	    static createFrom(source: any = {}) {
	        return new SetupRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.providerId = source["providerId"];
	        this.label = source["label"];
	        this.model = source["model"];
	        this.effort = source["effort"];
	        this.authMethod = source["authMethod"];
	        this.authorId = source["authorId"];
	        this.displayName = source["displayName"];
	    }
	}
	export class SetupState {
	    complete: boolean;
	    authorId: string;
	    displayName: string;
	    suggestedDisplayName: string;
	    providers: ProviderOption[];
	    efforts: EffortOption[];
	    dataPolicyNotice: string;
	
	    static createFrom(source: any = {}) {
	        return new SetupState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.complete = source["complete"];
	        this.authorId = source["authorId"];
	        this.displayName = source["displayName"];
	        this.suggestedDisplayName = source["suggestedDisplayName"];
	        this.providers = this.convertValues(source["providers"], ProviderOption);
	        this.efforts = this.convertValues(source["efforts"], EffortOption);
	        this.dataPolicyNotice = source["dataPolicyNotice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SignInView {
	    available: boolean;
	    state: string;
	    label?: string;
	    message?: string;
	    waitMinutes?: number;
	    account?: AccountView;
	
	    static createFrom(source: any = {}) {
	        return new SignInView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.state = source["state"];
	        this.label = source["label"];
	        this.message = source["message"];
	        this.waitMinutes = source["waitMinutes"];
	        this.account = this.convertValues(source["account"], AccountView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class StakeholderRequest {
	    id: string;
	    name: string;
	    org: string;
	
	    static createFrom(source: any = {}) {
	        return new StakeholderRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.org = source["org"];
	    }
	}
	export class StakeholderView {
	    id: string;
	    name: string;
	    org: string;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new StakeholderView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.org = source["org"];
	        this.label = source["label"];
	    }
	}
	export class StartupMode {
	    mode: string;
	    filePath?: string;
	    fileName?: string;
	
	    static createFrom(source: any = {}) {
	        return new StartupMode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.filePath = source["filePath"];
	        this.fileName = source["fileName"];
	    }
	}
	export class SyncCheckView {
	    ok: boolean;
	    failure?: SyncFailureView;
	    notice: string;
	
	    static createFrom(source: any = {}) {
	        return new SyncCheckView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.failure = this.convertValues(source["failure"], SyncFailureView);
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SyncChoiceOption {
	    choice: string;
	    label: string;
	    hint: string;
	
	    static createFrom(source: any = {}) {
	        return new SyncChoiceOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.choice = source["choice"];
	        this.label = source["label"];
	        this.hint = source["hint"];
	    }
	}
	export class SyncConflictView {
	    id: string;
	    label: string;
	    unitLabel: string;
	    categoryLabel: string;
	    theirsAuthor: string;
	    base: string;
	    theirs: string;
	    ours: string;
	
	    static createFrom(source: any = {}) {
	        return new SyncConflictView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.unitLabel = source["unitLabel"];
	        this.categoryLabel = source["categoryLabel"];
	        this.theirsAuthor = source["theirsAuthor"];
	        this.base = source["base"];
	        this.theirs = source["theirs"];
	        this.ours = source["ours"];
	    }
	}
	export class SyncCredentialKindOption {
	    kind: string;
	    label: string;
	    hint: string;
	
	    static createFrom(source: any = {}) {
	        return new SyncCredentialKindOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.label = source["label"];
	        this.hint = source["hint"];
	    }
	}
	export class SyncCredentialView {
	    projectId: string;
	    registered: boolean;
	    kind: string;
	    kindLabel: string;
	    state: string;
	    masked: string;
	    kinds: SyncCredentialKindOption[];
	
	    static createFrom(source: any = {}) {
	        return new SyncCredentialView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.registered = source["registered"];
	        this.kind = source["kind"];
	        this.kindLabel = source["kindLabel"];
	        this.state = source["state"];
	        this.masked = source["masked"];
	        this.kinds = this.convertValues(source["kinds"], SyncCredentialKindOption);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class SyncSummaryItemView {
	    categoryLabel: string;
	    added: number;
	    modified: number;
	    removed: number;
	    total: number;
	
	    static createFrom(source: any = {}) {
	        return new SyncSummaryItemView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.categoryLabel = source["categoryLabel"];
	        this.added = source["added"];
	        this.modified = source["modified"];
	        this.removed = source["removed"];
	        this.total = source["total"];
	    }
	}
	export class SyncIncorporateView {
	    done: boolean;
	    items?: SyncSummaryItemView[];
	    summary?: string;
	    conflicts?: SyncConflictView[];
	    conflictCount?: number;
	    failure?: SyncFailureView;
	    notice: string;
	
	    static createFrom(source: any = {}) {
	        return new SyncIncorporateView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.done = source["done"];
	        this.items = this.convertValues(source["items"], SyncSummaryItemView);
	        this.summary = source["summary"];
	        this.conflicts = this.convertValues(source["conflicts"], SyncConflictView);
	        this.conflictCount = source["conflictCount"];
	        this.failure = this.convertValues(source["failure"], SyncFailureView);
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SyncKindOption {
	    kind: string;
	    label: string;
	    hint: string;
	    requiresConsent: boolean;
	    consentText?: string;
	    requiresCredential: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SyncKindOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.label = source["label"];
	        this.hint = source["hint"];
	        this.requiresConsent = source["requiresConsent"];
	        this.consentText = source["consentText"];
	        this.requiresCredential = source["requiresCredential"];
	    }
	}
	export class SyncLogEntryView {
	    at: string;
	    author: string;
	    operation: string;
	    remote: string;
	    summary: string;
	    conflicts?: number;
	    result: string;
	    failure?: string;
	
	    static createFrom(source: any = {}) {
	        return new SyncLogEntryView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.at = source["at"];
	        this.author = source["author"];
	        this.operation = source["operation"];
	        this.remote = source["remote"];
	        this.summary = source["summary"];
	        this.conflicts = source["conflicts"];
	        this.result = source["result"];
	        this.failure = source["failure"];
	    }
	}
	export class SyncLogView {
	    entries?: SyncLogEntryView[];
	    lastIncorporation?: string;
	    notice?: string;
	
	    static createFrom(source: any = {}) {
	        return new SyncLogView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.entries = this.convertValues(source["entries"], SyncLogEntryView);
	        this.lastIncorporation = source["lastIncorporation"];
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SyncMergeOpenIssueView {
	    conflictId: string;
	    openIssueId: string;
	    notice: string;
	
	    static createFrom(source: any = {}) {
	        return new SyncMergeOpenIssueView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.conflictId = source["conflictId"];
	        this.openIssueId = source["openIssueId"];
	        this.notice = source["notice"];
	    }
	}
	export class SyncPublishPreviewView {
	    kindLabel: string;
	    location: string;
	    items?: SyncSummaryItemView[];
	    total: number;
	    summary: string;
	    firstPublish: boolean;
	    needsReattach: boolean;
	    restorePending: boolean;
	    failure?: SyncFailureView;
	    notice: string;
	
	    static createFrom(source: any = {}) {
	        return new SyncPublishPreviewView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kindLabel = source["kindLabel"];
	        this.location = source["location"];
	        this.items = this.convertValues(source["items"], SyncSummaryItemView);
	        this.total = source["total"];
	        this.summary = source["summary"];
	        this.firstPublish = source["firstPublish"];
	        this.needsReattach = source["needsReattach"];
	        this.restorePending = source["restorePending"];
	        this.failure = this.convertValues(source["failure"], SyncFailureView);
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SyncPublishView {
	    done: boolean;
	    items?: SyncSummaryItemView[];
	    summary?: string;
	    failure?: SyncFailureView;
	    notice: string;
	
	    static createFrom(source: any = {}) {
	        return new SyncPublishView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.done = source["done"];
	        this.items = this.convertValues(source["items"], SyncSummaryItemView);
	        this.summary = source["summary"];
	        this.failure = this.convertValues(source["failure"], SyncFailureView);
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SyncReattachView {
	    done: boolean;
	    items?: SyncSummaryItemView[];
	    summary?: string;
	    restorePending: boolean;
	    failure?: SyncFailureView;
	    notice: string;
	
	    static createFrom(source: any = {}) {
	        return new SyncReattachView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.done = source["done"];
	        this.items = this.convertValues(source["items"], SyncSummaryItemView);
	        this.summary = source["summary"];
	        this.restorePending = source["restorePending"];
	        this.failure = this.convertValues(source["failure"], SyncFailureView);
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SyncRemoteView {
	    configured: boolean;
	    kind?: string;
	    kindLabel?: string;
	    location?: string;
	    kinds: SyncKindOption[];
	    canManage: boolean;
	    manageReason?: string;
	    credential: SyncCredentialView;
	    notice?: string;
	
	    static createFrom(source: any = {}) {
	        return new SyncRemoteView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.configured = source["configured"];
	        this.kind = source["kind"];
	        this.kindLabel = source["kindLabel"];
	        this.location = source["location"];
	        this.kinds = this.convertValues(source["kinds"], SyncKindOption);
	        this.canManage = source["canManage"];
	        this.manageReason = source["manageReason"];
	        this.credential = this.convertValues(source["credential"], SyncCredentialView);
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SyncResolutionInput {
	    conflictId: string;
	    choice: string;
	    merged?: string;
	    adopt?: string;
	    openIssueId?: string;
	
	    static createFrom(source: any = {}) {
	        return new SyncResolutionInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.conflictId = source["conflictId"];
	        this.choice = source["choice"];
	        this.merged = source["merged"];
	        this.adopt = source["adopt"];
	        this.openIssueId = source["openIssueId"];
	    }
	}
	export class SyncStatusView {
	    configured: boolean;
	    kindLabel?: string;
	    location?: string;
	    available: boolean;
	    unavailableReason?: string;
	    canIncorporate: boolean;
	    canPublish: boolean;
	    publishReason?: string;
	    lastSyncedAt?: string;
	    lastIncorporationAt?: string;
	    hasUnpublished: boolean;
	    unpublishedSummary?: string;
	    credentialRequired: boolean;
	    credentialRegistered: boolean;
	    needsReattach: boolean;
	    restorePending: boolean;
	    notice?: string;
	
	    static createFrom(source: any = {}) {
	        return new SyncStatusView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.configured = source["configured"];
	        this.kindLabel = source["kindLabel"];
	        this.location = source["location"];
	        this.available = source["available"];
	        this.unavailableReason = source["unavailableReason"];
	        this.canIncorporate = source["canIncorporate"];
	        this.canPublish = source["canPublish"];
	        this.publishReason = source["publishReason"];
	        this.lastSyncedAt = source["lastSyncedAt"];
	        this.lastIncorporationAt = source["lastIncorporationAt"];
	        this.hasUnpublished = source["hasUnpublished"];
	        this.unpublishedSummary = source["unpublishedSummary"];
	        this.credentialRequired = source["credentialRequired"];
	        this.credentialRegistered = source["credentialRegistered"];
	        this.needsReattach = source["needsReattach"];
	        this.restorePending = source["restorePending"];
	        this.notice = source["notice"];
	    }
	}
	
	
	export class TokenUsageLatest {
	    at: string;
	    provider: string;
	    model: string;
	    session?: string;
	    hasTokens: boolean;
	    tokensIn: number;
	    tokensOut: number;
	    tokensReasoning: number;
	    tokensTotal: number;
	
	    static createFrom(source: any = {}) {
	        return new TokenUsageLatest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.at = source["at"];
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.session = source["session"];
	        this.hasTokens = source["hasTokens"];
	        this.tokensIn = source["tokensIn"];
	        this.tokensOut = source["tokensOut"];
	        this.tokensReasoning = source["tokensReasoning"];
	        this.tokensTotal = source["tokensTotal"];
	    }
	}
	export class TokenUsageRequest {
	    path?: string;
	
	    static createFrom(source: any = {}) {
	        return new TokenUsageRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	    }
	}
	export class UsageBucketView {
	    key: string;
	    tokens: number;
	    sends: number;
	    missing: number;
	
	    static createFrom(source: any = {}) {
	        return new UsageBucketView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.tokens = source["tokens"];
	        this.sends = source["sends"];
	        this.missing = source["missing"];
	    }
	}
	export class TokenUsageView {
	    path: string;
	    targetSystemName: string;
	    tokens: number;
	    tokensIn: number;
	    tokensOut: number;
	    tokensReasoning: number;
	    sends: number;
	    missingRecords: number;
	    lastUsedAt?: string;
	    byProvider: UsageBucketView[];
	    bySession: UsageBucketView[];
	    limitTokens?: number;
	    consumptionRatio?: number;
	    latest?: TokenUsageLatest;
	    scopeNotice?: string;
	    lastIncorporation?: string;
	
	    static createFrom(source: any = {}) {
	        return new TokenUsageView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.targetSystemName = source["targetSystemName"];
	        this.tokens = source["tokens"];
	        this.tokensIn = source["tokensIn"];
	        this.tokensOut = source["tokensOut"];
	        this.tokensReasoning = source["tokensReasoning"];
	        this.sends = source["sends"];
	        this.missingRecords = source["missingRecords"];
	        this.lastUsedAt = source["lastUsedAt"];
	        this.byProvider = this.convertValues(source["byProvider"], UsageBucketView);
	        this.bySession = this.convertValues(source["bySession"], UsageBucketView);
	        this.limitTokens = source["limitTokens"];
	        this.consumptionRatio = source["consumptionRatio"];
	        this.latest = this.convertValues(source["latest"], TokenUsageLatest);
	        this.scopeNotice = source["scopeNotice"];
	        this.lastIncorporation = source["lastIncorporation"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class UpdateProviderRequest {
	    label: string;
	    providerId: string;
	    model: string;
	    effort: string;
	    authMethod?: string;
	    makeDefault: boolean;
	
	    static createFrom(source: any = {}) {
	        return new UpdateProviderRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.providerId = source["providerId"];
	        this.model = source["model"];
	        this.effort = source["effort"];
	        this.authMethod = source["authMethod"];
	        this.makeDefault = source["makeDefault"];
	    }
	}
	export class UpdateState {
	    status: string;
	    currentVersion: string;
	    newVersion?: string;
	    message?: string;
	
	    static createFrom(source: any = {}) {
	        return new UpdateState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.currentVersion = source["currentVersion"];
	        this.newVersion = source["newVersion"];
	        this.message = source["message"];
	    }
	}
	
	export class UsageDetailView {
	    path: string;
	    targetSystemName: string;
	    tokens: number;
	    tokensIn: number;
	    tokensOut: number;
	    tokensReasoning: number;
	    sends: number;
	    missingRecords: number;
	    lastUsedAt?: string;
	    byProvider: UsageBucketView[];
	    bySession: UsageBucketView[];
	    byAuthor: UsageBucketView[];
	    limitTokens?: number;
	    consumptionRatio?: number;
	    sessions: docgen.SessionStats;
	    scopeNotice?: string;
	    lastIncorporation?: string;
	
	    static createFrom(source: any = {}) {
	        return new UsageDetailView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.targetSystemName = source["targetSystemName"];
	        this.tokens = source["tokens"];
	        this.tokensIn = source["tokensIn"];
	        this.tokensOut = source["tokensOut"];
	        this.tokensReasoning = source["tokensReasoning"];
	        this.sends = source["sends"];
	        this.missingRecords = source["missingRecords"];
	        this.lastUsedAt = source["lastUsedAt"];
	        this.byProvider = this.convertValues(source["byProvider"], UsageBucketView);
	        this.bySession = this.convertValues(source["bySession"], UsageBucketView);
	        this.byAuthor = this.convertValues(source["byAuthor"], UsageBucketView);
	        this.limitTokens = source["limitTokens"];
	        this.consumptionRatio = source["consumptionRatio"];
	        this.sessions = this.convertValues(source["sessions"], docgen.SessionStats);
	        this.scopeNotice = source["scopeNotice"];
	        this.lastIncorporation = source["lastIncorporation"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class UsageDashboardView {
	    from: string;
	    to: string;
	    projects: ProjectUsageRow[];
	    detail?: UsageDetailView;
	    markdown: string;
	
	    static createFrom(source: any = {}) {
	        return new UsageDashboardView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.projects = this.convertValues(source["projects"], ProjectUsageRow);
	        this.detail = this.convertValues(source["detail"], UsageDetailView);
	        this.markdown = source["markdown"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class UsageLimitRequest {
	    tokensMax: number;
	    warnRatio?: number;
	
	    static createFrom(source: any = {}) {
	        return new UsageLimitRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tokensMax = source["tokensMax"];
	        this.warnRatio = source["warnRatio"];
	    }
	}
	export class UsageReportRequest {
	    from: string;
	    to: string;
	    path?: string;
	
	    static createFrom(source: any = {}) {
	        return new UsageReportRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.path = source["path"];
	    }
	}
	export class UsageStatus {
	    consumedTokens: number;
	    missingRecords: number;
	    limitTokens?: number;
	    warnRatio: number;
	    remainingTokens?: number;
	    consumptionRatio?: number;
	    level: string;
	    scopeNotice?: string;
	
	    static createFrom(source: any = {}) {
	        return new UsageStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.consumedTokens = source["consumedTokens"];
	        this.missingRecords = source["missingRecords"];
	        this.limitTokens = source["limitTokens"];
	        this.warnRatio = source["warnRatio"];
	        this.remainingTokens = source["remainingTokens"];
	        this.consumptionRatio = source["consumptionRatio"];
	        this.level = source["level"];
	        this.scopeNotice = source["scopeNotice"];
	    }
	}
	export class UtteranceHitView {
	    sessionId: string;
	    phase: string;
	    type: string;
	    id: string;
	    speaker: string;
	    at: string;
	    status: string;
	    excerpt: string;
	
	    static createFrom(source: any = {}) {
	        return new UtteranceHitView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.phase = source["phase"];
	        this.type = source["type"];
	        this.id = source["id"];
	        this.speaker = source["speaker"];
	        this.at = source["at"];
	        this.status = source["status"];
	        this.excerpt = source["excerpt"];
	    }
	}
	export class UtteranceSearchView {
	    hits: UtteranceHitView[];
	    total: number;
	    truncated: boolean;
	    limit: number;
	
	    static createFrom(source: any = {}) {
	        return new UtteranceSearchView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hits = this.convertValues(source["hits"], UtteranceHitView);
	        this.total = source["total"];
	        this.truncated = source["truncated"];
	        this.limit = source["limit"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class VerifyResult {
	    ok: boolean;
	    reason?: string;
	    detail?: string;
	
	    static createFrom(source: any = {}) {
	        return new VerifyResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.reason = source["reason"];
	        this.detail = source["detail"];
	    }
	}
	export class WindowLockView {
	    target: string;
	    targetLabel: string;
	    acquiredAt?: string;
	    stale: boolean;
	    self: boolean;
	
	    static createFrom(source: any = {}) {
	        return new WindowLockView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.target = source["target"];
	        this.targetLabel = source["targetLabel"];
	        this.acquiredAt = source["acquiredAt"];
	        this.stale = source["stale"];
	        this.self = source["self"];
	    }
	}
	export class WorkModeOption {
	    mode: string;
	    label: string;
	    hint: string;
	
	    static createFrom(source: any = {}) {
	        return new WorkModeOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.label = source["label"];
	        this.hint = source["hint"];
	    }
	}
	export class WorkStart {
	    mode: string;
	    confirmed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new WorkStart(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.confirmed = source["confirmed"];
	    }
	}

}

export namespace dialogue {
	
	export class MergeResolution {
	    id: string;
	    baselineHash: string;
	    reference?: string;
	
	    static createFrom(source: any = {}) {
	        return new MergeResolution(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.baselineHash = source["baselineHash"];
	        this.reference = source["reference"];
	    }
	}
	export class TermCandidate {
	    term: string;
	    english: string;
	    definition: string;
	    evidence_refs?: string[];
	
	    static createFrom(source: any = {}) {
	        return new TermCandidate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.term = source["term"];
	        this.english = source["english"];
	        this.definition = source["definition"];
	        this.evidence_refs = source["evidence_refs"];
	    }
	}
	export class RequirementCandidate {
	    operation: string;
	    target_id?: string;
	    chapter: string;
	    title: string;
	    body_after: string;
	    acceptance_criteria?: string[];
	    evidence_refs: string[];
	    duplicate_of?: string;
	    id_group?: string;
	    kind?: string;
	    related_ids?: string[];
	    missing_evidence?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RequirementCandidate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.operation = source["operation"];
	        this.target_id = source["target_id"];
	        this.chapter = source["chapter"];
	        this.title = source["title"];
	        this.body_after = source["body_after"];
	        this.acceptance_criteria = source["acceptance_criteria"];
	        this.evidence_refs = source["evidence_refs"];
	        this.duplicate_of = source["duplicate_of"];
	        this.id_group = source["id_group"];
	        this.kind = source["kind"];
	        this.related_ids = source["related_ids"];
	        this.missing_evidence = source["missing_evidence"];
	    }
	}
	export class RequirementApproval {
	    candidate: RequirementCandidate;
	    group?: string;
	    priority?: string;
	    kind?: string;
	
	    static createFrom(source: any = {}) {
	        return new RequirementApproval(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.candidate = this.convertValues(source["candidate"], RequirementCandidate);
	        this.group = source["group"];
	        this.priority = source["priority"];
	        this.kind = source["kind"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class OpenIssueCandidate {
	    topic: string;
	    owner?: string;
	    due?: string;
	    needs_stakeholder?: boolean;
	    blocks_requirement_ids?: string[];
	    evidence_refs: string[];
	    duplicate_of?: string;
	    related_ids?: string[];
	    missing_evidence?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new OpenIssueCandidate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.topic = source["topic"];
	        this.owner = source["owner"];
	        this.due = source["due"];
	        this.needs_stakeholder = source["needs_stakeholder"];
	        this.blocks_requirement_ids = source["blocks_requirement_ids"];
	        this.evidence_refs = source["evidence_refs"];
	        this.duplicate_of = source["duplicate_of"];
	        this.related_ids = source["related_ids"];
	        this.missing_evidence = source["missing_evidence"];
	    }
	}
	export class DecisionCandidate {
	    topic_key: string;
	    body: string;
	    rationale: string;
	    evidence_refs: string[];
	    supersedes_decision_id?: string;
	    duplicate_of?: string;
	    related_ids?: string[];
	    missing_evidence?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DecisionCandidate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.topic_key = source["topic_key"];
	        this.body = source["body"];
	        this.rationale = source["rationale"];
	        this.evidence_refs = source["evidence_refs"];
	        this.supersedes_decision_id = source["supersedes_decision_id"];
	        this.duplicate_of = source["duplicate_of"];
	        this.related_ids = source["related_ids"];
	        this.missing_evidence = source["missing_evidence"];
	    }
	}
	export class DecisionApproval {
	    candidate: DecisionCandidate;
	    resolvesIssueIds?: string[];
	
	    static createFrom(source: any = {}) {
	        return new DecisionApproval(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.candidate = this.convertValues(source["candidate"], DecisionCandidate);
	        this.resolvesIssueIds = source["resolvesIssueIds"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ApprovalRequest {
	    decisions?: DecisionApproval[];
	    openIssues?: OpenIssueCandidate[];
	    requirementUpdates?: RequirementApproval[];
	    termCandidates?: TermCandidate[];
	    merges?: MergeResolution[];
	
	    static createFrom(source: any = {}) {
	        return new ApprovalRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.decisions = this.convertValues(source["decisions"], DecisionApproval);
	        this.openIssues = this.convertValues(source["openIssues"], OpenIssueCandidate);
	        this.requirementUpdates = this.convertValues(source["requirementUpdates"], RequirementApproval);
	        this.termCandidates = this.convertValues(source["termCandidates"], TermCandidate);
	        this.merges = this.convertValues(source["merges"], MergeResolution);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class UnsatisfiedItem {
	    itemId: string;
	    name: string;
	    topicKey: string;
	
	    static createFrom(source: any = {}) {
	        return new UnsatisfiedItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.itemId = source["itemId"];
	        this.name = source["name"];
	        this.topicKey = source["topicKey"];
	    }
	}
	export class ChapterCompleteness {
	    chapterId: string;
	    name: string;
	    percent: number;
	    satisfied: number;
	    total: number;
	    openIssues: number;
	    state: string;
	    unsatisfiedItems?: UnsatisfiedItem[];
	
	    static createFrom(source: any = {}) {
	        return new ChapterCompleteness(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.chapterId = source["chapterId"];
	        this.name = source["name"];
	        this.percent = source["percent"];
	        this.satisfied = source["satisfied"];
	        this.total = source["total"];
	        this.openIssues = source["openIssues"];
	        this.state = source["state"];
	        this.unsatisfiedItems = this.convertValues(source["unsatisfiedItems"], UnsatisfiedItem);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Confirmation {
	    confirmable: boolean;
	    noRequirements?: boolean;
	    draftRequirements?: string[];
	    blockingIssues?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Confirmation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.confirmable = source["confirmable"];
	        this.noRequirements = source["noRequirements"];
	        this.draftRequirements = source["draftRequirements"];
	        this.blockingIssues = source["blockingIssues"];
	    }
	}
	export class Contradiction {
	    with_decision_id: string;
	    description: string;
	    evidence_refs?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Contradiction(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.with_decision_id = source["with_decision_id"];
	        this.description = source["description"];
	        this.evidence_refs = source["evidence_refs"];
	    }
	}
	export class ContradictionView {
	    decisionId: string;
	    decisionBody: string;
	    description: string;
	    evidenceRefs?: string[];
	
	    static createFrom(source: any = {}) {
	        return new ContradictionView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.decisionId = source["decisionId"];
	        this.decisionBody = source["decisionBody"];
	        this.description = source["description"];
	        this.evidenceRefs = source["evidenceRefs"];
	    }
	}
	
	
	export class PerspectiveCandidate {
	    name: string;
	    summary: string;
	    evidence_refs?: string[];
	    duplicate_of?: string;
	    missing_evidence?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PerspectiveCandidate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.summary = source["summary"];
	        this.evidence_refs = source["evidence_refs"];
	        this.duplicate_of = source["duplicate_of"];
	        this.missing_evidence = source["missing_evidence"];
	    }
	}
	export class Extraction {
	    decisions: DecisionCandidate[];
	    open_issues: OpenIssueCandidate[];
	    requirement_updates: RequirementCandidate[];
	    term_candidates: TermCandidate[];
	    contradictions: Contradiction[];
	    perspective_candidates?: PerspectiveCandidate[];
	
	    static createFrom(source: any = {}) {
	        return new Extraction(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.decisions = this.convertValues(source["decisions"], DecisionCandidate);
	        this.open_issues = this.convertValues(source["open_issues"], OpenIssueCandidate);
	        this.requirement_updates = this.convertValues(source["requirement_updates"], RequirementCandidate);
	        this.term_candidates = this.convertValues(source["term_candidates"], TermCandidate);
	        this.contradictions = this.convertValues(source["contradictions"], Contradiction);
	        this.perspective_candidates = this.convertValues(source["perspective_candidates"], PerspectiveCandidate);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FeedbackEntry {
	    heading: string;
	    startLine: number;
	    endLine: number;
	    kind: string;
	    relatedIds?: string[];
	    body: string;
	    impact?: string;
	
	    static createFrom(source: any = {}) {
	        return new FeedbackEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.heading = source["heading"];
	        this.startLine = source["startLine"];
	        this.endLine = source["endLine"];
	        this.kind = source["kind"];
	        this.relatedIds = source["relatedIds"];
	        this.body = source["body"];
	        this.impact = source["impact"];
	    }
	}
	export class FeedbackAnalysis {
	    importId: string;
	    kind: string;
	    sourceName: string;
	    extraction?: Extraction;
	    chunkCount: number;
	    estimatedTokens: number;
	    failedChunks?: number[];
	    fallback: boolean;
	    notice?: string;
	    fromTemplate: boolean;
	    entries?: FeedbackEntry[];
	    templateNotice?: string;
	    revertTargets?: string[];
	
	    static createFrom(source: any = {}) {
	        return new FeedbackAnalysis(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.importId = source["importId"];
	        this.kind = source["kind"];
	        this.sourceName = source["sourceName"];
	        this.extraction = this.convertValues(source["extraction"], Extraction);
	        this.chunkCount = source["chunkCount"];
	        this.estimatedTokens = source["estimatedTokens"];
	        this.failedChunks = source["failedChunks"];
	        this.fallback = source["fallback"];
	        this.notice = source["notice"];
	        this.fromTemplate = source["fromTemplate"];
	        this.entries = this.convertValues(source["entries"], FeedbackEntry);
	        this.templateNotice = source["templateNotice"];
	        this.revertTargets = source["revertTargets"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FeedbackApproval {
	    decisions?: DecisionApproval[];
	    openIssues?: OpenIssueCandidate[];
	    requirementUpdates?: RequirementApproval[];
	    termCandidates?: TermCandidate[];
	    merges?: MergeResolution[];
	    perspectives?: PerspectiveCandidate[];
	    revertConfirmed?: string[];
	
	    static createFrom(source: any = {}) {
	        return new FeedbackApproval(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.decisions = this.convertValues(source["decisions"], DecisionApproval);
	        this.openIssues = this.convertValues(source["openIssues"], OpenIssueCandidate);
	        this.requirementUpdates = this.convertValues(source["requirementUpdates"], RequirementApproval);
	        this.termCandidates = this.convertValues(source["termCandidates"], TermCandidate);
	        this.merges = this.convertValues(source["merges"], MergeResolution);
	        this.perspectives = this.convertValues(source["perspectives"], PerspectiveCandidate);
	        this.revertConfirmed = source["revertConfirmed"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class UnknownAnswer {
	    questionId: string;
	    answerRef: string;
	    sourceIssueId: string;
	    // Go type: time
	    answeredAt: any;
	    reason: string;
	    ownerCandidate?: string;
	    currentOwner?: string;
	
	    static createFrom(source: any = {}) {
	        return new UnknownAnswer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.questionId = source["questionId"];
	        this.answerRef = source["answerRef"];
	        this.sourceIssueId = source["sourceIssueId"];
	        this.answeredAt = this.convertValues(source["answeredAt"], null);
	        this.reason = source["reason"];
	        this.ownerCandidate = source["ownerCandidate"];
	        this.currentOwner = source["currentOwner"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RequirementDiff {
	    operation: string;
	    targetId?: string;
	    title: string;
	    bodyBefore: string;
	    bodyAfter: string;
	
	    static createFrom(source: any = {}) {
	        return new RequirementDiff(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.operation = source["operation"];
	        this.targetId = source["targetId"];
	        this.title = source["title"];
	        this.bodyBefore = source["bodyBefore"];
	        this.bodyAfter = source["bodyAfter"];
	    }
	}
	export class ImportAnalysis {
	    questionnaireId: string;
	    extraction?: Extraction;
	    requirementDiffs?: RequirementDiff[];
	    contradictions?: ContradictionView[];
	    unknownAnswers?: UnknownAnswer[];
	    affectedRequirements?: Record<string, Array<string>>;
	    sourceIssueIds?: string[];
	    fallback: boolean;
	    notice?: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportAnalysis(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.questionnaireId = source["questionnaireId"];
	        this.extraction = this.convertValues(source["extraction"], Extraction);
	        this.requirementDiffs = this.convertValues(source["requirementDiffs"], RequirementDiff);
	        this.contradictions = this.convertValues(source["contradictions"], ContradictionView);
	        this.unknownAnswers = this.convertValues(source["unknownAnswers"], UnknownAnswer);
	        this.affectedRequirements = source["affectedRequirements"];
	        this.sourceIssueIds = source["sourceIssueIds"];
	        this.fallback = source["fallback"];
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class OwnerUpdate {
	    issueId: string;
	    owner: string;
	
	    static createFrom(source: any = {}) {
	        return new OwnerUpdate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.issueId = source["issueId"];
	        this.owner = source["owner"];
	    }
	}
	export class UnknownAnswerNote {
	    issueId: string;
	    answerRef: string;
	    // Go type: time
	    answeredAt: any;
	    reason: string;
	
	    static createFrom(source: any = {}) {
	        return new UnknownAnswerNote(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.issueId = source["issueId"];
	        this.answerRef = source["answerRef"];
	        this.answeredAt = this.convertValues(source["answeredAt"], null);
	        this.reason = source["reason"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ImportApproval {
	    decisions?: DecisionApproval[];
	    openIssues?: OpenIssueCandidate[];
	    requirementUpdates?: RequirementApproval[];
	    termCandidates?: TermCandidate[];
	    merges?: MergeResolution[];
	    unknownNotes?: UnknownAnswerNote[];
	    ownerUpdates?: OwnerUpdate[];
	
	    static createFrom(source: any = {}) {
	        return new ImportApproval(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.decisions = this.convertValues(source["decisions"], DecisionApproval);
	        this.openIssues = this.convertValues(source["openIssues"], OpenIssueCandidate);
	        this.requirementUpdates = this.convertValues(source["requirementUpdates"], RequirementApproval);
	        this.termCandidates = this.convertValues(source["termCandidates"], TermCandidate);
	        this.merges = this.convertValues(source["merges"], MergeResolution);
	        this.unknownNotes = this.convertValues(source["unknownNotes"], UnknownAnswerNote);
	        this.ownerUpdates = this.convertValues(source["ownerUpdates"], OwnerUpdate);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ImportChunk {
	    index: number;
	    startLine: number;
	    endLine: number;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportChunk(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.startLine = source["startLine"];
	        this.endLine = source["endLine"];
	        this.text = source["text"];
	    }
	}
	export class ImportSendPreview {
	    importId: string;
	    kind: string;
	    sourceName: string;
	    system: string;
	    context: string;
	    chunks: ImportChunk[];
	    chunkCount: number;
	    estimatedTokens: number;
	    labels?: string[];
	    tooLarge: boolean;
	    notice?: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportSendPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.importId = source["importId"];
	        this.kind = source["kind"];
	        this.sourceName = source["sourceName"];
	        this.system = source["system"];
	        this.context = source["context"];
	        this.chunks = this.convertValues(source["chunks"], ImportChunk);
	        this.chunkCount = source["chunkCount"];
	        this.estimatedTokens = source["estimatedTokens"];
	        this.labels = source["labels"];
	        this.tooLarge = source["tooLarge"];
	        this.notice = source["notice"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	
	export class PresentedQuestion {
	    topicKey: string;
	    text: string;
	    followUpIndex: number;
	    answered: boolean;
	    utteranceId?: string;
	    reconfirm?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PresentedQuestion(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.topicKey = source["topicKey"];
	        this.text = source["text"];
	        this.followUpIndex = source["followUpIndex"];
	        this.answered = source["answered"];
	        this.utteranceId = source["utteranceId"];
	        this.reconfirm = source["reconfirm"];
	    }
	}
	
	
	
	export class ResumeContext {
	    state: string;
	    presentedQuestion?: PresentedQuestion;
	    recentSummary?: string;
	    openIssues?: projectstore.OpenIssue[];
	    hasPendingCandidates: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ResumeContext(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.presentedQuestion = this.convertValues(source["presentedQuestion"], PresentedQuestion);
	        this.recentSummary = source["recentSummary"];
	        this.openIssues = this.convertValues(source["openIssues"], projectstore.OpenIssue);
	        this.hasPendingCandidates = source["hasPendingCandidates"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	

}

export namespace docgen {
	
	export class DiffLine {
	    kind: string;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new DiffLine(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.text = source["text"];
	    }
	}
	export class ChapterDiff {
	    fileName: string;
	    chapter: string;
	    status: string;
	    added: number;
	    removed: number;
	    lines?: DiffLine[];
	
	    static createFrom(source: any = {}) {
	        return new ChapterDiff(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fileName = source["fileName"];
	        this.chapter = source["chapter"];
	        this.status = source["status"];
	        this.added = source["added"];
	        this.removed = source["removed"];
	        this.lines = this.convertValues(source["lines"], DiffLine);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class Violation {
	    check: string;
	    severity: string;
	    file?: string;
	    line?: number;
	    target?: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new Violation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.check = source["check"];
	        this.severity = source["severity"];
	        this.file = source["file"];
	        this.line = source["line"];
	        this.target = source["target"];
	        this.message = source["message"];
	    }
	}
	export class VerifyResult {
	    violations: Violation[];
	
	    static createFrom(source: any = {}) {
	        return new VerifyResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.violations = this.convertValues(source["violations"], Violation);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ExportResult {
	    files: string[];
	    verification: VerifyResult;
	    exported: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ExportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.files = source["files"];
	        this.verification = this.convertValues(source["verification"], VerifyResult);
	        this.exported = source["exported"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PhaseSessionCount {
	    phase: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new PhaseSessionCount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.phase = source["phase"];
	        this.count = source["count"];
	    }
	}
	export class QuestionnaireFlow {
	    id: string;
	    addressee: string;
	    // Go type: time
	    issuedAt: any;
	    // Go type: time
	    importedAt: any;
	    elapsedDays: number;
	
	    static createFrom(source: any = {}) {
	        return new QuestionnaireFlow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.addressee = source["addressee"];
	        this.issuedAt = this.convertValues(source["issuedAt"], null);
	        this.importedAt = this.convertValues(source["importedAt"], null);
	        this.elapsedDays = source["elapsedDays"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SessionStats {
	    // Go type: time
	    from: any;
	    // Go type: time
	    to: any;
	    sessionsTotal: number;
	    sessionsByPhase: PhaseSessionCount[];
	    questionnairesIssued: number;
	    questionnairesImported: number;
	    importedFlows: QuestionnaireFlow[];
	    elapsedDaysAverage: number;
	    decisionsApproved: number;
	    openIssuesResolved: number;
	    requirementsConfirmed: number;
	
	    static createFrom(source: any = {}) {
	        return new SessionStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = this.convertValues(source["from"], null);
	        this.to = this.convertValues(source["to"], null);
	        this.sessionsTotal = source["sessionsTotal"];
	        this.sessionsByPhase = this.convertValues(source["sessionsByPhase"], PhaseSessionCount);
	        this.questionnairesIssued = source["questionnairesIssued"];
	        this.questionnairesImported = source["questionnairesImported"];
	        this.importedFlows = this.convertValues(source["importedFlows"], QuestionnaireFlow);
	        this.elapsedDaysAverage = source["elapsedDaysAverage"];
	        this.decisionsApproved = source["decisionsApproved"];
	        this.openIssuesResolved = source["openIssuesResolved"];
	        this.requirementsConfirmed = source["requirementsConfirmed"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	

}

export namespace exchange {
	
	export class ImportConfirmation {
	    AllowReimport: boolean;
	    AcceptModifiedContent: boolean;
	    AcceptAddresseeMismatch: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ImportConfirmation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.AllowReimport = source["AllowReimport"];
	        this.AcceptModifiedContent = source["AcceptModifiedContent"];
	        this.AcceptAddresseeMismatch = source["AcceptAddresseeMismatch"];
	    }
	}

}

export namespace guide {
	
	export class Stage {
	    id: string;
	    label: string;
	    purpose: string;
	    screen: string;
	    target: string;
	
	    static createFrom(source: any = {}) {
	        return new Stage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.purpose = source["purpose"];
	        this.screen = source["screen"];
	        this.target = source["target"];
	    }
	}

}

export namespace importer {
	
	export class ClassificationCount {
	    classification: string;
	    label: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new ClassificationCount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.classification = source["classification"];
	        this.label = source["label"];
	        this.count = source["count"];
	    }
	}
	export class FeedbackSummary {
	    // Go type: time
	    from: any;
	    // Go type: time
	    to: any;
	    total: number;
	    counts: ClassificationCount[];
	    unclassified: number;
	
	    static createFrom(source: any = {}) {
	        return new FeedbackSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = this.convertValues(source["from"], null);
	        this.to = this.convertValues(source["to"], null);
	        this.total = source["total"];
	        this.counts = this.convertValues(source["counts"], ClassificationCount);
	        this.unclassified = source["unclassified"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Meta {
	    ID: string;
	    Kind: string;
	    // Go type: time
	    ImportedAt: any;
	    SourceName: string;
	    SourceFormat: string;
	    ExtractionStatus: string;
	    FromTemplate?: boolean;
	    Classification: string;
	
	    static createFrom(source: any = {}) {
	        return new Meta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Kind = source["Kind"];
	        this.ImportedAt = this.convertValues(source["ImportedAt"], null);
	        this.SourceName = source["SourceName"];
	        this.SourceFormat = source["SourceFormat"];
	        this.ExtractionStatus = source["ExtractionStatus"];
	        this.FromTemplate = source["FromTemplate"];
	        this.Classification = source["Classification"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RefLocation {
	    ref: string;
	    importId: string;
	    sourceName: string;
	    kind: string;
	    format: string;
	    importedAt: string;
	    sourcePath: string;
	    startLine: number;
	    endLine: number;
	    excerpt: string[];
	    contextStartLine: number;
	    context: string[];
	
	    static createFrom(source: any = {}) {
	        return new RefLocation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ref = source["ref"];
	        this.importId = source["importId"];
	        this.sourceName = source["sourceName"];
	        this.kind = source["kind"];
	        this.format = source["format"];
	        this.importedAt = source["importedAt"];
	        this.sourcePath = source["sourcePath"];
	        this.startLine = source["startLine"];
	        this.endLine = source["endLine"];
	        this.excerpt = source["excerpt"];
	        this.contextStartLine = source["contextStartLine"];
	        this.context = source["context"];
	    }
	}

}

export namespace projectstore {
	
	export class EvidenceCitation {
	    recordId: string;
	    recordKind: string;
	    title: string;
	    ref: string;
	
	    static createFrom(source: any = {}) {
	        return new EvidenceCitation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.recordId = source["recordId"];
	        this.recordKind = source["recordKind"];
	        this.title = source["title"];
	        this.ref = source["ref"];
	    }
	}
	export class MissingEvidenceRecord {
	    recordId: string;
	    recordKind: string;
	    title: string;
	
	    static createFrom(source: any = {}) {
	        return new MissingEvidenceRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.recordId = source["recordId"];
	        this.recordKind = source["recordKind"];
	        this.title = source["title"];
	    }
	}
	export class OpenIssue {
	    ID: string;
	    Owner: string;
	    Due: string;
	    Status: string;
	    NeedsStakeholder: boolean;
	    Evidence: string[];
	    ResolvedBy: string;
	    Body: string;
	
	    static createFrom(source: any = {}) {
	        return new OpenIssue(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Owner = source["Owner"];
	        this.Due = source["Due"];
	        this.Status = source["Status"];
	        this.NeedsStakeholder = source["NeedsStakeholder"];
	        this.Evidence = source["Evidence"];
	        this.ResolvedBy = source["ResolvedBy"];
	        this.Body = source["Body"];
	    }
	}

}

