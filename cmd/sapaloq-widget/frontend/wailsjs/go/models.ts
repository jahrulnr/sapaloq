export namespace main {
	
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

