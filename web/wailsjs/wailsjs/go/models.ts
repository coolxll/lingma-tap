export namespace bridge {
	
	export class ModelInfo {
	    key: string;
	    display_name: string;
	    format: string;
	    source: string;
	    order: number;
	    is_vl: boolean;
	    is_reasoning: boolean;
	    max_input_tokens: number;
	
	    static createFrom(source: any = {}) {
	        return new ModelInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.display_name = source["display_name"];
	        this.format = source["format"];
	        this.source = source["source"];
	        this.order = source["order"];
	        this.is_vl = source["is_vl"];
	        this.is_reasoning = source["is_reasoning"];
	        this.max_input_tokens = source["max_input_tokens"];
	    }
	}

}

export namespace proto {
	
	export class SSEEvent {
	    event_type: string;
	    data: string;
	    body?: string;
	
	    static createFrom(source: any = {}) {
	        return new SSEEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.event_type = source["event_type"];
	        this.data = source["data"];
	        this.body = source["body"];
	    }
	}
	export class GatewayLog {
	    id: number;
	    ts: string;
	    session: string;
	    model: string;
	    method: string;
	    path: string;
	    request_body: string;
	    response_body: string;
	    input_tokens: number;
	    output_tokens: number;
	    cached_tokens?: number;
	    reasoning_tokens?: number;
	    total_tokens?: number;
	    ttft?: number;
	    upstream_attempts?: number;
	    recovery_applied?: boolean;
	    upstream_error_class?: string;
	    first_actionable_ms?: number;
	    reasoning_only_bytes?: number;
	    requested_profile?: string;
	    effective_profile?: string;
	    context_trimmed?: boolean;
	    context_original_bytes?: number;
	    context_trimmed_bytes?: number;
	    responses_degraded?: boolean;
	    responses_warnings?: string;
	    status: number;
	    latency: number;
	    error?: string;
	    is_sse: boolean;
	    sse_events?: SSEEvent[];
	    finish_reason?: string;
	
	    static createFrom(source: any = {}) {
	        return new GatewayLog(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.ts = source["ts"];
	        this.session = source["session"];
	        this.model = source["model"];
	        this.method = source["method"];
	        this.path = source["path"];
	        this.request_body = source["request_body"];
	        this.response_body = source["response_body"];
	        this.input_tokens = source["input_tokens"];
	        this.output_tokens = source["output_tokens"];
	        this.cached_tokens = source["cached_tokens"];
	        this.reasoning_tokens = source["reasoning_tokens"];
	        this.total_tokens = source["total_tokens"];
	        this.ttft = source["ttft"];
	        this.upstream_attempts = source["upstream_attempts"];
	        this.recovery_applied = source["recovery_applied"];
	        this.upstream_error_class = source["upstream_error_class"];
	        this.first_actionable_ms = source["first_actionable_ms"];
	        this.reasoning_only_bytes = source["reasoning_only_bytes"];
	        this.requested_profile = source["requested_profile"];
	        this.effective_profile = source["effective_profile"];
	        this.context_trimmed = source["context_trimmed"];
	        this.context_original_bytes = source["context_original_bytes"];
	        this.context_trimmed_bytes = source["context_trimmed_bytes"];
	        this.responses_degraded = source["responses_degraded"];
	        this.responses_warnings = source["responses_warnings"];
	        this.status = source["status"];
	        this.latency = source["latency"];
	        this.error = source["error"];
	        this.is_sse = source["is_sse"];
	        this.sse_events = this.convertValues(source["sse_events"], SSEEvent);
	        this.finish_reason = source["finish_reason"];
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
	export class GatewayLogStats {
	    total: number;
	    input_tokens: number;
	    output_tokens: number;
	    cached_tokens: number;
	    reasoning_tokens: number;
	    total_tokens: number;
	
	    static createFrom(source: any = {}) {
	        return new GatewayLogStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.input_tokens = source["input_tokens"];
	        this.output_tokens = source["output_tokens"];
	        this.cached_tokens = source["cached_tokens"];
	        this.reasoning_tokens = source["reasoning_tokens"];
	        this.total_tokens = source["total_tokens"];
	    }
	}
	export class Record {
	    id: number;
	    ts: string;
	    session: string;
	    index: number;
	    direction: string;
	    source: string;
	    method: string;
	    url: string;
	    host: string;
	    path: string;
	    is_encoded: boolean;
	    endpoint_type: string;
	    request_headers: Record<string, string>;
	    request_body: string;
	    request_body_raw: string;
	    request_mime: string;
	    request_size: number;
	    status: number;
	    status_text: string;
	    response_headers: Record<string, string>;
	    response_body: string;
	    response_body_raw: string;
	    response_mime: string;
	    response_size: number;
	    is_sse: boolean;
	    sse_events?: SSEEvent[];
	    body_phase?: string;
	    body_complete: boolean;
	    body_truncated: boolean;
	    captured_size: number;
	    declared_size?: number;
	    body_encoding?: string;
	    content_encoding?: string;
	    correlation_keys?: string[];
	    artifact_ids?: number[];
	    error?: string;
	    model?: string;
	    input_tokens?: number;
	    output_tokens?: number;
	    cached_tokens?: number;
	    reasoning_tokens?: number;
	    total_tokens?: number;
	    ttft?: number;
	    latency?: number;
	    finish_reason?: string;
	    upstream_attempts?: number;
	    recovery_applied?: boolean;
	    upstream_error_class?: string;
	    first_actionable_ms?: number;
	    reasoning_only_bytes?: number;
	    requested_profile?: string;
	    effective_profile?: string;
	
	    static createFrom(source: any = {}) {
	        return new Record(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.ts = source["ts"];
	        this.session = source["session"];
	        this.index = source["index"];
	        this.direction = source["direction"];
	        this.source = source["source"];
	        this.method = source["method"];
	        this.url = source["url"];
	        this.host = source["host"];
	        this.path = source["path"];
	        this.is_encoded = source["is_encoded"];
	        this.endpoint_type = source["endpoint_type"];
	        this.request_headers = source["request_headers"];
	        this.request_body = source["request_body"];
	        this.request_body_raw = source["request_body_raw"];
	        this.request_mime = source["request_mime"];
	        this.request_size = source["request_size"];
	        this.status = source["status"];
	        this.status_text = source["status_text"];
	        this.response_headers = source["response_headers"];
	        this.response_body = source["response_body"];
	        this.response_body_raw = source["response_body_raw"];
	        this.response_mime = source["response_mime"];
	        this.response_size = source["response_size"];
	        this.is_sse = source["is_sse"];
	        this.sse_events = this.convertValues(source["sse_events"], SSEEvent);
	        this.body_phase = source["body_phase"];
	        this.body_complete = source["body_complete"];
	        this.body_truncated = source["body_truncated"];
	        this.captured_size = source["captured_size"];
	        this.declared_size = source["declared_size"];
	        this.body_encoding = source["body_encoding"];
	        this.content_encoding = source["content_encoding"];
	        this.correlation_keys = source["correlation_keys"];
	        this.artifact_ids = source["artifact_ids"];
	        this.error = source["error"];
	        this.model = source["model"];
	        this.input_tokens = source["input_tokens"];
	        this.output_tokens = source["output_tokens"];
	        this.cached_tokens = source["cached_tokens"];
	        this.reasoning_tokens = source["reasoning_tokens"];
	        this.total_tokens = source["total_tokens"];
	        this.ttft = source["ttft"];
	        this.latency = source["latency"];
	        this.finish_reason = source["finish_reason"];
	        this.upstream_attempts = source["upstream_attempts"];
	        this.recovery_applied = source["recovery_applied"];
	        this.upstream_error_class = source["upstream_error_class"];
	        this.first_actionable_ms = source["first_actionable_ms"];
	        this.reasoning_only_bytes = source["reasoning_only_bytes"];
	        this.requested_profile = source["requested_profile"];
	        this.effective_profile = source["effective_profile"];
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

export namespace updater {
	
	export class Info {
	    supported: boolean;
	    available: boolean;
	    current_version: string;
	    latest_version: string;
	    release_url: string;
	    asset_name: string;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.supported = source["supported"];
	        this.available = source["available"];
	        this.current_version = source["current_version"];
	        this.latest_version = source["latest_version"];
	        this.release_url = source["release_url"];
	        this.asset_name = source["asset_name"];
	    }
	}

}

