export namespace bridge {
	
	export class StreamEvent {
	    kind: string;
	    session_id?: string;
	    delta?: string;
	    tool_call?: parse.ToolCall;
	    leak?: string;
	    error?: string;
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
	
	export class chatResult {
	    ok: boolean;
	    session_id?: string;
	    events: bridge.StreamEvent[];
	
	    static createFrom(source: any = {}) {
	        return new chatResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.session_id = source["session_id"];
	        this.events = this.convertValues(source["events"], bridge.StreamEvent);
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

