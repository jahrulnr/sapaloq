export namespace bridge {
	
	export class TranscriptEntry {
	    id: string;
	    kind: string;
	    generation_id?: string;
	    turn_id?: number;
	    seq?: number;
	    // Go type: time
	    at: any;
	    archived?: boolean;
	    text?: string;
	    tool_id?: string;
	    tool_name?: string;
	    tool_args?: string;
	    tool_result?: string;
	    tool_status?: string;
	    task_id?: string;
	    task_role?: string;
	    task_status?: string;
	    summary?: string;
	    checkpoint_index?: number;
	    checkpoint_reason?: string;
	    label?: string;
	    wait_seconds?: number;
	
	    static createFrom(source: any = {}) {
	        return new TranscriptEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.generation_id = source["generation_id"];
	        this.turn_id = source["turn_id"];
	        this.seq = source["seq"];
	        this.at = this.convertValues(source["at"], null);
	        this.archived = source["archived"];
	        this.text = source["text"];
	        this.tool_id = source["tool_id"];
	        this.tool_name = source["tool_name"];
	        this.tool_args = source["tool_args"];
	        this.tool_result = source["tool_result"];
	        this.tool_status = source["tool_status"];
	        this.task_id = source["task_id"];
	        this.task_role = source["task_role"];
	        this.task_status = source["task_status"];
	        this.summary = source["summary"];
	        this.checkpoint_index = source["checkpoint_index"];
	        this.checkpoint_reason = source["checkpoint_reason"];
	        this.label = source["label"];
	        this.wait_seconds = source["wait_seconds"];
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
	export class chatHistoryResult {
	    ok: boolean;
	    session_id: string;
	    transcript?: bridge.TranscriptEntry[];
	    usage?: chatUsage;
	
	    static createFrom(source: any = {}) {
	        return new chatHistoryResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.session_id = source["session_id"];
	        this.transcript = this.convertValues(source["transcript"], bridge.TranscriptEntry);
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
	    generation_id?: string;
	    transcript?: bridge.TranscriptEntry[];
	    usage?: chatUsage;
	    reset?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new chatResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.session_id = source["session_id"];
	        this.generation_id = source["generation_id"];
	        this.transcript = this.convertValues(source["transcript"], bridge.TranscriptEntry);
	        this.usage = this.convertValues(source["usage"], chatUsage);
	        this.reset = source["reset"];
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
	
	export class clipboardImage {
	    data_uri: string;
	    mime: string;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new clipboardImage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.data_uri = source["data_uri"];
	        this.mime = source["mime"];
	        this.size = source["size"];
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
	export class sessionDeleteResult {
	    ok: boolean;
	    session_id: string;
	    reset?: boolean;
	    transcript?: bridge.TranscriptEntry[];
	
	    static createFrom(source: any = {}) {
	        return new sessionDeleteResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.session_id = source["session_id"];
	        this.reset = source["reset"];
	        this.transcript = this.convertValues(source["transcript"], bridge.TranscriptEntry);
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
	    transcript?: bridge.TranscriptEntry[];
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
	        this.transcript = this.convertValues(source["transcript"], bridge.TranscriptEntry);
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

