export namespace bridge {
	
	export class StreamEvent {
	    kind: string;
	    session_id?: string;
	    delta?: string;
	    tool_call?: parse.ToolCall;
	    leak?: string;
	    error?: string;
	    status?: string;
	    wait_seconds?: number;
	    task_id?: string;
	    task_role?: string;
	    task_status?: string;
	    summary?: string;
	    run_id?: string;
	    job_id?: string;
	    parent_id?: string;
	    target_id?: string;
	    event_id?: string;
	    correlation_id?: string;
	    version?: number;
	    checkpoint_index?: number;
	    checkpoint_reason?: string;
	    checkpoint_summary?: string;
	    // Go type: time
	    at: any;
	
	    static createFrom(source: any = {}) {
	        return new StreamEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.session_id = source["session_id"];
	        this.delta = source["delta"];
	        this.tool_call = this.convertValues(source["tool_call"], parse.ToolCall);
	        this.leak = source["leak"];
	        this.error = source["error"];
	        this.status = source["status"];
	        this.wait_seconds = source["wait_seconds"];
	        this.task_id = source["task_id"];
	        this.task_role = source["task_role"];
	        this.task_status = source["task_status"];
	        this.summary = source["summary"];
	        this.run_id = source["run_id"];
	        this.job_id = source["job_id"];
	        this.parent_id = source["parent_id"];
	        this.target_id = source["target_id"];
	        this.event_id = source["event_id"];
	        this.correlation_id = source["correlation_id"];
	        this.version = source["version"];
	        this.checkpoint_index = source["checkpoint_index"];
	        this.checkpoint_reason = source["checkpoint_reason"];
	        this.checkpoint_summary = source["checkpoint_summary"];
	        this.at = this.convertValues(source["at"], null);
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

export namespace config {
	
	export class CommandEntry {
	    id: string;
	    prefix: string;
	    pattern: string;
	    label: string;
	    description: string;
	    category: string;
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CommandEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.prefix = source["prefix"];
	        this.pattern = source["pattern"];
	        this.label = source["label"];
	        this.description = source["description"];
	        this.category = source["category"];
	        this.enabled = source["enabled"];
	    }
	}

}

export namespace main {
	
	export class actorRuntimeStatus {
	    id: string;
	    role: string;
	    status: string;
	    phase: string;
	    workspace: string;
	
	    static createFrom(source: any = {}) {
	        return new actorRuntimeStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.role = source["role"];
	        this.status = source["status"];
	        this.phase = source["phase"];
	        this.workspace = source["workspace"];
	    }
	}
	export class chatUsage {
	    session_id: string;
	    used_tokens: number;
	    context_window: number;
	    percent: number;
	    provider: string;
	    model: string;
	    compacted_turns: number;
	    active_turns: number;
	
	    static createFrom(source: any = {}) {
	        return new chatUsage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.session_id = source["session_id"];
	        this.used_tokens = source["used_tokens"];
	        this.context_window = source["context_window"];
	        this.percent = source["percent"];
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.compacted_turns = source["compacted_turns"];
	        this.active_turns = source["active_turns"];
	    }
	}
	export class chatTurn {
	    id: number;
	    seq: number;
	    role: string;
	    content: string;
	    checkpoint_index?: number;
	    created_at?: string;
	    archived?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new chatTurn(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.seq = source["seq"];
	        this.role = source["role"];
	        this.content = source["content"];
	        this.checkpoint_index = source["checkpoint_index"];
	        this.created_at = source["created_at"];
	        this.archived = source["archived"];
	    }
	}
	export class chatHistoryResult {
	    ok: boolean;
	    session_id: string;
	    turns: chatTurn[];
	    timeline?: bridge.StreamEvent[];
	    usage?: chatUsage;
	
	    static createFrom(source: any = {}) {
	        return new chatHistoryResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.session_id = source["session_id"];
	        this.turns = this.convertValues(source["turns"], chatTurn);
	        this.timeline = this.convertValues(source["timeline"], bridge.StreamEvent);
	        this.usage = this.convertValues(source["usage"], chatUsage);
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
	export class chatResult {
	    ok: boolean;
	    session_id?: string;
	    events: bridge.StreamEvent[];
	    usage?: chatUsage;
	
	    static createFrom(source: any = {}) {
	        return new chatResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.session_id = source["session_id"];
	        this.events = this.convertValues(source["events"], bridge.StreamEvent);
	        this.usage = this.convertValues(source["usage"], chatUsage);
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
	
	
	export class droppedFile {
	    path?: string;
	    name: string;
	    mime: string;
	    size: number;
	    data_uri?: string;
	    text?: string;
	    is_image: boolean;
	    is_dir: boolean;
	
	    static createFrom(source: any = {}) {
	        return new droppedFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.mime = source["mime"];
	        this.size = source["size"];
	        this.data_uri = source["data_uri"];
	        this.text = source["text"];
	        this.is_image = source["is_image"];
	        this.is_dir = source["is_dir"];
	    }
	}
	export class pingResult {
	    ok: boolean;
	    message: string;
	    ring_state: string;
	    server_ms: number;
	    round_trip_ms: number;
	    socket_path: string;
	
	    static createFrom(source: any = {}) {
	        return new pingResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.message = source["message"];
	        this.ring_state = source["ring_state"];
	        this.server_ms = source["server_ms"];
	        this.round_trip_ms = source["round_trip_ms"];
	        this.socket_path = source["socket_path"];
	    }
	}
	export class runtimeStatus {
	    provider: string;
	    model: string;
	    driver: string;
	    reasoning?: string;
	    config_path: string;
	    data_path: string;
	    memory_path: string;
	    state_path: string;
	    workspace_path: string;
	    actors: actorRuntimeStatus[];
	
	    static createFrom(source: any = {}) {
	        return new runtimeStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.driver = source["driver"];
	        this.reasoning = source["reasoning"];
	        this.config_path = source["config_path"];
	        this.data_path = source["data_path"];
	        this.memory_path = source["memory_path"];
	        this.state_path = source["state_path"];
	        this.workspace_path = source["workspace_path"];
	        this.actors = this.convertValues(source["actors"], actorRuntimeStatus);
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
	export class sessionSummary {
	    id: string;
	    title: string;
	    active: boolean;
	    turn_count: number;
	    updated_at: string;
	    created_at: string;
	
	    static createFrom(source: any = {}) {
	        return new sessionSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.active = source["active"];
	        this.turn_count = source["turn_count"];
	        this.updated_at = source["updated_at"];
	        this.created_at = source["created_at"];
	    }
	}
	export class sessionListResult {
	    ok: boolean;
	    sessions: sessionSummary[];
	
	    static createFrom(source: any = {}) {
	        return new sessionListResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.sessions = this.convertValues(source["sessions"], sessionSummary);
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
	
	export class taskInspectEvent {
	    kind: string;
	    delta?: string;
	    tool_name?: string;
	    tool_arguments?: string;
	    status?: string;
	    task_status?: string;
	    summary?: string;
	    error?: string;
	    checkpoint_index?: number;
	    checkpoint_reason?: string;
	    at: string;
	
	    static createFrom(source: any = {}) {
	        return new taskInspectEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.delta = source["delta"];
	        this.tool_name = source["tool_name"];
	        this.tool_arguments = source["tool_arguments"];
	        this.status = source["status"];
	        this.task_status = source["task_status"];
	        this.summary = source["summary"];
	        this.error = source["error"];
	        this.checkpoint_index = source["checkpoint_index"];
	        this.checkpoint_reason = source["checkpoint_reason"];
	        this.at = source["at"];
	    }
	}
	export class taskInspectResult {
	    id: string;
	    role: string;
	    status: string;
	    task: string;
	    result?: string;
	    error?: string;
	    question?: string;
	    plan_task_id?: string;
	    plan?: string;
	    events: taskInspectEvent[];
	    event_count: number;
	    updated_at: string;
	
	    static createFrom(source: any = {}) {
	        return new taskInspectResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.role = source["role"];
	        this.status = source["status"];
	        this.task = source["task"];
	        this.result = source["result"];
	        this.error = source["error"];
	        this.question = source["question"];
	        this.plan_task_id = source["plan_task_id"];
	        this.plan = source["plan"];
	        this.events = this.convertValues(source["events"], taskInspectEvent);
	        this.event_count = source["event_count"];
	        this.updated_at = source["updated_at"];
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

export namespace parse {
	
	export class ToolCall {
	    id?: string;
	    name: string;
	    arguments?: number[];
	    source?: string;
	
	    static createFrom(source: any = {}) {
	        return new ToolCall(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.arguments = source["arguments"];
	        this.source = source["source"];
	    }
	}

}

