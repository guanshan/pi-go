package extensions

const scriptRuntimeBridge = `
import { registerHooks } from "node:module";
import { createInterface } from "node:readline";
import { pathToFileURL } from "node:url";
import { inspect } from "node:util";

const extensionPath = process.argv[process.argv.length - 1];
const tools = new Map();
const commands = new Map();
const shortcuts = new Map();
const providers = new Map();
const providerHandlers = new Map();
const messageRenderers = new Map();
const autocompleteProviders = [];
const autocompleteControllers = new Map();
const providerControllers = new Map();
const flags = new Map();
const eventHandlers = new Map();
const shutdownHandlers = [];
let flagValues = {};
let bridgeReady = false;
try {
	flagValues = JSON.parse(process.env.PI_EXTENSION_FLAG_VALUES || "{}") || {};
} catch {
	flagValues = {};
}

for (const level of ["log", "info", "warn", "error"]) {
	console[level] = (...args) => {
		process.stderr.write(args.map((arg) => typeof arg === "string" ? arg : inspect(arg, { depth: 4 })).join(" ") + "\n");
	};
}

function write(message) {
	process.stdout.write(JSON.stringify(message) + "\n");
}

function shortcutMetadata(shortcut) {
	return { key: shortcut.key, description: shortcut.description ?? "" };
}

function providerMetadata(provider) {
	return {
		api: provider.api ?? "",
		providerName: provider.providerName ?? "",
		hasHandler: provider.hasHandler === true,
		modelConfig: provider.modelConfig,
	};
}

function messageRendererMetadata(renderer) {
	return { customType: renderer.customType };
}

function notifyShortcutRegistered(shortcut) {
	if (bridgeReady) write({ type: "shortcut_registered", shortcut: shortcutMetadata(shortcut) });
}

function notifyShortcutUnregistered(key) {
	if (bridgeReady) write({ type: "shortcut_unregistered", key });
}

function notifyProviderRegistered(provider) {
	if (bridgeReady) write({ type: "provider_registered", provider: providerMetadata(provider) });
}

function notifyProviderUnregistered(providerOrName) {
	if (!bridgeReady) return;
	if (providerOrName && typeof providerOrName === "object") {
		write({ type: "provider_unregistered", api: providerOrName.api ?? "", providerName: providerOrName.providerName ?? providerOrName.key ?? "" });
		return;
	}
	write({ type: "provider_unregistered", providerName: String(providerOrName ?? "") });
}

function notifyMessageRendererRegistered(renderer) {
	if (bridgeReady) write({ type: "message_renderer_registered", renderer: messageRendererMetadata(renderer) });
}

function notifyMessageRendererUnregistered(customType) {
	if (bridgeReady) write({ type: "message_renderer_unregistered", customType });
}

// Server-initiated UI requests (ctx.ui.*): emit a ui_request keyed by a string
// uiId and resolve when the host writes back the matching ui_response. The uiId
// namespace is disjoint from the integer ids the host uses for execute_* requests.
let uiRequestSeq = 0;
const uiPending = new Map();
// hasUIState tracks whether the host has a UI handler bound. Seeded at spawn and
// updated live via set_has_ui, so ctx.hasUI reflects late handler binding.
let hasUIState = process.env.PI_EXTENSION_HAS_UI === "1";
function sendUIRequest(method, params) {
	return new Promise((resolve, reject) => {
		const uiId = "ui-" + (++uiRequestSeq);
		uiPending.set(uiId, { resolve, reject });
		write({ type: "ui_request", uiId, method, params: params ?? {} });
	});
}

let contextActionSeq = 0;
const contextActionPending = new Map();
function sendContextAction(action, params) {
	return new Promise((resolve, reject) => {
		const actionId = "ctx-" + (++contextActionSeq);
		contextActionPending.set(actionId, { resolve, reject });
		write({ type: "context_action_request", actionId, action, params: params ?? {} });
	});
}

function warnContextAction(action, error) {
	console.warn("ctx." + action + " failed: " + (error?.message ?? String(error)));
}

function typeModuleSource() {
	return [
		"const optionalKey = \"__piOptional\";",
		"function clean(schema) {",
		"  if (!schema || typeof schema !== \"object\") return schema;",
		"  const out = { ...schema };",
		"  delete out[optionalKey];",
		"  return out;",
		"}",
		"export const Type = {",
		"  Object(properties = {}, options = {}) {",
		"    const required = [];",
		"    const cleaned = {};",
		"    for (const [key, schema] of Object.entries(properties ?? {})) {",
		"      if (!schema || schema[optionalKey] !== true) required.push(key);",
		"      cleaned[key] = clean(schema);",
		"    }",
		"    const result = { type: \"object\", properties: cleaned, additionalProperties: false, ...options };",
		"    if (required.length > 0) result.required = required;",
		"    return result;",
		"  },",
		"  String(options = {}) { return { type: \"string\", ...options }; },",
		"  Number(options = {}) { return { type: \"number\", ...options }; },",
		"  Integer(options = {}) { return { type: \"integer\", ...options }; },",
		"  Boolean(options = {}) { return { type: \"boolean\", ...options }; },",
		"  Array(items = {}, options = {}) { return { type: \"array\", items, ...options }; },",
		"  Optional(schema = {}) { return { ...schema, [optionalKey]: true }; },",
		"  Literal(value, options = {}) { return { const: value, ...options }; },",
		"  Union(anyOf = [], options = {}) { return { anyOf, ...options }; },",
		"  Record(keySchema = {}, valueSchema = {}, options = {}) { return { type: \"object\", additionalProperties: valueSchema, ...options }; },",
		"  Any(options = {}) { return { ...options }; },",
		"  Unknown(options = {}) { return { ...options }; },",
		"  Null(options = {}) { return { type: \"null\", ...options }; },",
		"};",
		"export function StringEnum(values, options = {}) {",
		"  return { type: \"string\", enum: Array.from(values ?? []), ...options };",
		"}",
		"export default { Type, StringEnum };",
	].join("\n");
}

const virtualModules = new Map([
	["pi-virtual:ai", typeModuleSource()],
	["pi-virtual:typebox", typeModuleSource()],
	["pi-virtual:coding-agent", [
		"export function defineTool(definition) { return definition; }",
		"export function getSettingsListTheme() {",
		"  const identity = (text) => String(text ?? \"\");",
		"  return { label: identity, value: identity, description: identity, cursor: \"> \", hint: identity };",
		"}",
		"export function createEventBus() {",
		"  const listeners = new Map();",
		"  return {",
		"    on(event, listener) {",
		"      const list = listeners.get(event) ?? [];",
		"      list.push(listener);",
		"      listeners.set(event, list);",
		"      return () => listeners.set(event, (listeners.get(event) ?? []).filter((item) => item !== listener));",
		"    },",
		"    emit(event, payload) {",
		"      for (const listener of listeners.get(event) ?? []) listener(payload);",
		"    },",
		"  };",
		"}",
		"export default { defineTool, createEventBus, getSettingsListTheme };",
	].join("\n")],
	["pi-virtual:tui", [
		"export function matchesKey(data, key) { return data === key; }",
		"export function truncateToWidth(value, width) { const text = String(value ?? \"\"); return text.length > width ? text.slice(0, Math.max(0, width)) : text; }",
		"function childLines(child, width) { return typeof child?.render === \"function\" ? child.render(width) : [String(child ?? \"\")]; }",
		"export class Text { constructor(text = \"\") { this.text = text; } render() { return String(this.text).split(\"\\n\"); } }",
		"export class Container { constructor(children = []) { this.children = Array.isArray(children) ? children : []; } addChild(child) { this.children.push(child); return child; } render(width) { return this.children.flatMap((child) => childLines(child, width)); } }",
		"export class Box extends Container { constructor(...args) { super([]); this.styleFn = args.find((a) => typeof a === \"function\"); } render(width) { const lines = super.render(width); return typeof this.styleFn === \"function\" ? lines.map((l) => { try { return String(this.styleFn(l)); } catch { return l; } }) : lines; } }",
		"export class Spacer { render() { return [\"\"]; } }",
		"export class Input {}",
		"export class SelectList {}",
		"export class SettingsList {}",
		"export class Loader {}",
		"export class CancellableLoader {}",
		"export class Markdown { constructor(markdown = \"\") { this.markdown = markdown; } render() { return String(this.markdown).split(\"\\n\"); } }",
		// Key mirrors @earendil-works/pi-tui's Key: named key constants plus modifier
		// helpers (e.g. Key.ctrlAlt('p') -> 'ctrl+alt+p'). The backtick key uses the
		// \\u0060 escape because the surrounding bridge is a Go raw string literal.
		"export const Key = (() => {",
		"  const k = { escape: \"escape\", esc: \"esc\", enter: \"enter\", return: \"return\", tab: \"tab\", space: \"space\", backspace: \"backspace\", delete: \"delete\", insert: \"insert\", clear: \"clear\", home: \"home\", end: \"end\", pageUp: \"pageUp\", pageDown: \"pageDown\", up: \"up\", down: \"down\", left: \"left\", right: \"right\", f1: \"f1\", f2: \"f2\", f3: \"f3\", f4: \"f4\", f5: \"f5\", f6: \"f6\", f7: \"f7\", f8: \"f8\", f9: \"f9\", f10: \"f10\", f11: \"f11\", f12: \"f12\" };",
		"  Object.assign(k, { backtick: \"\\u0060\", hyphen: \"-\", equals: \"=\", leftbracket: \"[\", rightbracket: \"]\", backslash: \"\\\\\", semicolon: \";\", quote: \"'\", comma: \",\", period: \".\", slash: \"/\", exclamation: \"!\", at: \"@\", hash: \"#\", dollar: \"$\", percent: \"%\", caret: \"^\", ampersand: \"&\", asterisk: \"*\", leftparen: \"(\", rightparen: \")\", underscore: \"_\", plus: \"+\", pipe: \"|\", tilde: \"~\", leftbrace: \"{\", rightbrace: \"}\", colon: \":\", lessthan: \"<\", greaterthan: \">\", question: \"?\" });",
		"  const mods = { ctrl: \"ctrl\", shift: \"shift\", alt: \"alt\", super: \"super\", ctrlShift: \"ctrl+shift\", shiftCtrl: \"shift+ctrl\", ctrlAlt: \"ctrl+alt\", altCtrl: \"alt+ctrl\", shiftAlt: \"shift+alt\", altShift: \"alt+shift\", ctrlSuper: \"ctrl+super\", superCtrl: \"super+ctrl\", shiftSuper: \"shift+super\", superShift: \"super+shift\", altSuper: \"alt+super\", superAlt: \"super+alt\", ctrlShiftAlt: \"ctrl+shift+alt\", ctrlShiftSuper: \"ctrl+shift+super\" };",
		"  for (const name of Object.keys(mods)) { const prefix = mods[name]; k[name] = (key) => prefix + \"+\" + key; }",
		"  return k;",
		"})();",
		"export default { matchesKey, truncateToWidth, Text, Container, Box, Spacer, Input, SelectList, SettingsList, Loader, CancellableLoader, Markdown, Key };",
	].join("\n")],
]);

function virtualURL(specifier) {
	if (specifier === "@earendil-works/pi-ai" || specifier.startsWith("@earendil-works/pi-ai/")) return "pi-virtual:ai";
	if (specifier === "typebox") return "pi-virtual:typebox";
	if (specifier === "@earendil-works/pi-coding-agent" || specifier.startsWith("@earendil-works/pi-coding-agent/")) return "pi-virtual:coding-agent";
	if (specifier === "@earendil-works/pi-tui" || specifier.startsWith("@earendil-works/pi-tui/")) return "pi-virtual:tui";
	return "";
}

if (typeof registerHooks !== "function") {
	throw new Error("Node.js module.registerHooks is required for script extension loading");
}

registerHooks({
	resolve(specifier, context, nextResolve) {
		const url = virtualURL(specifier);
		if (url) return { url, shortCircuit: true };
		return nextResolve(specifier, context);
	},
	load(url, context, nextLoad) {
		if (virtualModules.has(url)) {
			return { format: "module", source: virtualModules.get(url), shortCircuit: true };
		}
		return nextLoad(url, context);
	},
});

function normalizeToolResult(result) {
	if (result == null) return { content: [], isError: false };
	if (typeof result === "string") return { content: [{ type: "text", text: result }], isError: false };
	const out = { ...result };
	if (typeof out.content === "string") out.content = [{ type: "text", text: out.content }];
	if (!Array.isArray(out.content)) out.content = [];
	out.isError = Boolean(out.isError);
	return out;
}

function normalizeProviderContentBlock(block) {
	if (block == null) return null;
	if (typeof block === "string") return { type: "text", text: block };
	if (typeof block !== "object") return { type: "text", text: String(block) };
	const out = { ...block };
	if (!out.type) out.type = out.name ? "toolCall" : "text";
	if (out.type === "text" && out.text == null) out.text = String(out.content ?? "");
	if (out.type === "toolCall" && out.arguments && typeof out.arguments === "object") {
		out.arguments = JSON.stringify(out.arguments);
	}
	return out;
}

function normalizeProviderContent(value) {
	if (value == null) return [];
	if (typeof value === "string") return [{ type: "text", text: value }];
	if (Array.isArray(value)) return value.map(normalizeProviderContentBlock).filter(Boolean);
	if (typeof value === "object" && (value.type || value.text != null || value.name)) {
		const block = normalizeProviderContentBlock(value);
		return block ? [block] : [];
	}
	return [{ type: "text", text: String(value) }];
}

function normalizeProviderResult(result) {
	if (result == null) return { content: [], usage: {}, stopReason: "stop", errorMessage: "", toolCalls: [] };
	if (typeof result === "string") return { content: [{ type: "text", text: result }], usage: {}, stopReason: "stop", errorMessage: "", toolCalls: [] };
	if (Array.isArray(result)) return { content: normalizeProviderContent(result), usage: {}, stopReason: "stop", errorMessage: "", toolCalls: [] };
	if (typeof result !== "object") return { content: [{ type: "text", text: String(result) }], usage: {}, stopReason: "stop", errorMessage: "", toolCalls: [] };
	const message = result.message && typeof result.message === "object" ? result.message : undefined;
	const source = message ?? result;
	let contentValue = source.content ?? source.blocks ?? source.delta ?? source.text ?? "";
	if (contentValue && typeof contentValue === "object" && !Array.isArray(contentValue) && contentValue.role) {
		contentValue = contentValue.content ?? "";
	}
	const toolCalls = Array.isArray(source.toolCalls ?? result.toolCalls) ? (source.toolCalls ?? result.toolCalls) : [];
	let content = normalizeProviderContent(contentValue);
	if (content.length === 0 && toolCalls.length > 0) {
		content = toolCalls.map((call) => normalizeProviderContentBlock({
			type: "toolCall",
			id: call.id ?? "",
			name: call.name ?? "",
			arguments: call.arguments ?? "{}",
			thoughtSignature: call.thoughtSignature,
		})).filter(Boolean);
	}
	return {
		content,
		usage: source.usage ?? result.usage ?? {},
		stopReason: source.stopReason ?? source.reason ?? source.finishReason ?? result.stopReason ?? "stop",
		errorMessage: source.errorMessage ?? result.errorMessage ?? "",
		responseId: source.responseId ?? result.responseId ?? "",
		responseModel: source.responseModel ?? result.responseModel ?? "",
		diagnostics: Array.isArray(source.diagnostics ?? result.diagnostics) ? (source.diagnostics ?? result.diagnostics) : [],
		toolCalls,
	};
}

const PROVIDER_EVENT_TYPES = new Set([
	"start", "text_start", "text_delta", "text_end", "thinking_start", "thinking_delta",
	"thinking_end", "toolcall_start", "toolcall_delta", "toolcall_end", "done", "error",
]);

// streamProviderResult drains a provider handler's result and writes the final
// integer-id reply (the authoritative message, accumulated exactly as the prior
// collect-to-final behavior so non-stream parity is preserved). When wantsStream
// is set and the result is an async/sync iterable, it ALSO emits out-of-band
// provider_chunk events for token-level display: AssistantMessageEvent-shaped
// chunks pass through (minus the terminal start/done/error, which the Go side
// drives from the final reply); raw string/object chunks are synthesized into
// text_start/text_delta/text_end and toolcall_start/toolcall_end events.
async function streamProviderResult(id, result, wantsStream) {
	result = await result;
	const iterable = result != null && typeof result !== "string" && !Array.isArray(result) &&
		(typeof result[Symbol.asyncIterator] === "function" || typeof result[Symbol.iterator] === "function");
	if (!iterable) {
		write({ id, ok: true, result: normalizeProviderResult(result) });
		return;
	}
	let text = "";
	const content = [];
	let final = { usage: {}, stopReason: "stop", errorMessage: "", toolCalls: [] };
	let textIndex = -1;
	let nextIndex = 0;
	for await (const chunk of result) {
		const passthrough = chunk && typeof chunk === "object" && typeof chunk.type === "string" && PROVIDER_EVENT_TYPES.has(chunk.type);
		const item = normalizeProviderResult(chunk);
		if (wantsStream) {
			if (passthrough) {
				if (chunk.type !== "start" && chunk.type !== "done" && chunk.type !== "error") {
					write({ type: "provider_chunk", callId: id, event: { type: chunk.type, contentIndex: chunk.contentIndex, delta: chunk.delta, content: chunk.content, toolCall: chunk.toolCall } });
				}
			} else if (item.content.length === 1 && item.content[0].type === "text") {
				const delta = String(item.content[0].text ?? item.content[0].delta ?? "");
				if (textIndex < 0) { textIndex = nextIndex++; write({ type: "provider_chunk", callId: id, event: { type: "text_start", contentIndex: textIndex } }); }
				write({ type: "provider_chunk", callId: id, event: { type: "text_delta", contentIndex: textIndex, delta } });
			} else {
				for (const block of item.content) {
					if (block && block.type === "toolCall") {
						const ti = nextIndex++;
						write({ type: "provider_chunk", callId: id, event: { type: "toolcall_start", contentIndex: ti } });
						write({ type: "provider_chunk", callId: id, event: { type: "toolcall_end", contentIndex: ti, toolCall: { id: block.id, name: block.name, arguments: block.arguments } } });
					}
				}
			}
		}
		final = { ...final, ...item, content: undefined };
		if (item.content.length === 1 && item.content[0].type === "text") {
			text += String(item.content[0].text ?? item.content[0].delta ?? "");
		} else {
			content.push(...item.content);
		}
	}
	if (wantsStream && textIndex >= 0) {
		write({ type: "provider_chunk", callId: id, event: { type: "text_end", contentIndex: textIndex, content: text } });
	}
	if (text) content.unshift({ type: "text", text });
	write({ id, ok: true, result: { ...final, content } });
}

function providerKeyName(apiOrProvider, maybeProvider) {
	if (typeof apiOrProvider === "string") return apiOrProvider.trim();
	const provider = maybeProvider ?? apiOrProvider;
	return String(provider?.providerName ?? provider?.provider ?? provider?.name ?? provider?.id ?? provider?.api ?? "").trim();
}

function providerAPIName(apiOrProvider, maybeProvider) {
	const provider = maybeProvider ?? apiOrProvider;
	if (provider && typeof provider === "object" && provider.api != null) return String(provider.api ?? "").trim();
	if (typeof apiOrProvider === "string") return apiOrProvider.trim();
	return String(provider?.name ?? provider?.id ?? "").trim();
}

const providerModelConfigSkipKeys = new Set(["complete", "completeChat", "generate", "stream", "streamChat", "completeSimple", "completeText", "streamSimple", "streamText", "provider", "providerName", "id", "key", "hasHandler", "modelConfig"]);
function providerModelConfig(provider) {
	if (!provider || typeof provider !== "object") return undefined;
	const modelConfig = {};
	for (const [key, value] of Object.entries(provider)) {
		if (value !== undefined && typeof value !== "function" && !providerModelConfigSkipKeys.has(key)) modelConfig[key] = value;
	}
	const hasConfig = (Array.isArray(modelConfig.models) && modelConfig.models.length > 0) || (modelConfig.modelOverrides && typeof modelConfig.modelOverrides === "object" && Object.keys(modelConfig.modelOverrides).length > 0) || !!modelConfig.baseUrl || !!modelConfig.apiKey || (modelConfig.headers && typeof modelConfig.headers === "object" && Object.keys(modelConfig.headers).length > 0) || !!modelConfig.compat || Object.keys(modelConfig).some((key) => key !== "name" && key !== "api");
	return hasConfig ? modelConfig : undefined;
}

function normalizeProviderDefinition(apiOrProvider, maybeProvider) {
	const provider = maybeProvider ?? apiOrProvider;
	const providerName = providerKeyName(apiOrProvider, maybeProvider);
	const apiName = providerAPIName(apiOrProvider, maybeProvider);
	if (!providerName && !apiName) {
		console.warn("pi.registerProvider expected a provider name or api; skipping.");
		return null;
	}
	if (!provider || typeof provider !== "object") {
		console.warn("pi.registerProvider expected a provider object for " + (providerName || apiName) + "; skipping.");
		return null;
	}
	const complete = provider.complete ?? provider.completeChat ?? provider.generate;
	const stream = provider.stream ?? provider.streamChat;
	const completeSimple = provider.completeSimple ?? provider.completeText;
	const streamSimple = provider.streamSimple ?? provider.streamText;
	const hasHandler = [complete, stream, completeSimple, streamSimple].some((handler) => typeof handler === "function");
	const modelConfig = providerModelConfig(provider);
	if (!hasHandler && !modelConfig) {
		console.warn("pi.registerProvider ignored " + (providerName || apiName) + " because it has no callable complete/stream handler or model config.");
		return null;
	}
	if (hasHandler && !apiName) {
		console.warn("pi.registerProvider ignored " + providerName + " because handler providers require an api.");
		return null;
	}
	return {
		key: providerName || apiName,
		providerName: providerName || apiName,
		api: apiName,
		hasHandler,
		modelConfig,
		complete: typeof complete === "function" ? complete : undefined,
		stream: typeof stream === "function" ? stream : undefined,
		completeSimple: typeof completeSimple === "function" ? completeSimple : undefined,
		streamSimple: typeof streamSimple === "function" ? streamSimple : undefined,
	};
}

function providerHandlerFor(provider, request) {
	const wantsStream = request.stream === true;
	const wantsSimple = request.simple === true;
	if (wantsStream && wantsSimple && typeof provider.streamSimple === "function") return provider.streamSimple;
	if (wantsSimple && typeof provider.completeSimple === "function") return provider.completeSimple;
	if (wantsStream && typeof provider.stream === "function") return provider.stream;
	if (typeof provider.complete === "function") return provider.complete;
	if (typeof provider.stream === "function") return provider.stream;
	if (typeof provider.completeSimple === "function") return provider.completeSimple;
	if (typeof provider.streamSimple === "function") return provider.streamSimple;
	return undefined;
}

function messageRendererCustomType(customType) {
	return String(customType ?? "").trim();
}

function normalizeMessageRenderer(customTypeOrRenderer, maybeHandler) {
	let customType = customTypeOrRenderer;
	let handler = maybeHandler;
	if (customTypeOrRenderer && typeof customTypeOrRenderer === "object") {
		customType = customTypeOrRenderer.customType ?? customTypeOrRenderer.type ?? customTypeOrRenderer.name;
		handler = maybeHandler ?? customTypeOrRenderer.handler ?? customTypeOrRenderer.render;
	}
	if (handler && typeof handler === "object") {
		handler = handler.handler ?? handler.render;
	}
	const type = messageRendererCustomType(customType);
	if (!type) {
		console.warn("pi.registerMessageRenderer expected a customType; skipping.");
		return null;
	}
	if (typeof handler !== "function") {
		console.warn("pi.registerMessageRenderer expected a render handler for " + type + "; skipping.");
		return null;
	}
	return { customType: type, handler };
}

// messageRendererTheme approximates the TS Theme styling helpers by emitting real
// ANSI SGR codes (the Go TUI is ANSI-width-aware, so styled lines render and wrap
// correctly). The Node child has no access to the live Go theme's colors, so
// semantic color names map to a fixed 16-color table and unknown names degrade to
// no styling (TS throws on unknown colors; crashing a renderer mid-transcript is
// worse). bold/dim/italic/underline are exact SGR.
const messageRendererFGColors = {
	error: 31, danger: 31, red: 31, success: 32, green: 32, warning: 33, yellow: 33,
	info: 34, blue: 34, accent: 35, primary: 35, magenta: 35, secondary: 36, cyan: 36,
	muted: 90, dim: 90, gray: 90, grey: 90, white: 37, black: 30,
};
function messageRendererColorCode(color, base) {
	const name = String(color ?? "").trim().toLowerCase();
	const fg = messageRendererFGColors[name];
	if (fg === undefined) return null;
	// base 0 -> foreground codes as-is; base 10 -> background (30->40, 90->100).
	return fg + base;
}
function messageRendererWrap(open, close, text) {
	return "\x1b[" + open + "m" + String(text ?? "") + "\x1b[" + close + "m";
}
const messageRendererTheme = {
	fg(color, text) {
		const code = messageRendererColorCode(color, 0);
		return code == null ? String(text ?? "") : messageRendererWrap(code, 39, text);
	},
	bg(color, text) {
		const code = messageRendererColorCode(color, 10);
		return code == null ? String(text ?? "") : messageRendererWrap(code, 49, text);
	},
	bold(text) { return messageRendererWrap(1, 22, text); },
	dim(text) { return messageRendererWrap(2, 22, text); },
	italic(text) { return messageRendererWrap(3, 23, text); },
	underline(text) { return messageRendererWrap(4, 24, text); },
};

async function normalizeMessageRendererResult(result, width) {
	result = await result;
	if (result == null) return { lines: [] };
	if (typeof result === "string") return { lines: result.split("\n") };
	if (Array.isArray(result)) {
		return { lines: result.flatMap((item) => typeof item === "string" ? item.split("\n") : [String(item ?? "")]) };
	}
	if (typeof result?.render === "function") {
		return normalizeMessageRendererResult(await result.render(width), width);
	}
	if (Array.isArray(result.lines)) {
		return { lines: result.lines.map((line) => String(line ?? "")) };
	}
	if (result.text != null) {
		return { lines: String(result.text).split("\n") };
	}
	return { lines: [String(result)] };
}

function normalizeOutgoingCustomMessage(message, options = {}) {
	const messageObject = message && typeof message === "object" ? message : {};
	const optionObject = options && typeof options === "object" ? options : {};
	const customType = messageRendererCustomType(
		typeof message === "string" ? message : messageObject.customType ?? messageObject.type ?? messageObject.name
	);
	if (!customType) {
		console.warn("pi.sendMessage expected a customType; skipping.");
		return null;
	}
	let content = messageObject.content;
	if (content === undefined) content = messageObject.text;
	if (content === undefined && typeof message === "string") content = optionObject.content ?? optionObject.text;
	let details = messageObject.details;
	if (details === undefined) details = optionObject.details;
	const display = messageObject.display === false || optionObject.display === false ? false : true;
	return {
		message: { customType, content, display, details },
		options: { triggerTurn: messageObject.triggerTurn === true || optionObject.triggerTurn === true },
	};
}

function normalizeSendUserMessage(content, options = {}) {
	const optionObject = options && typeof options === "object" ? options : {};
	return {
		content,
		options: {
			deliverAs: optionObject.deliverAs,
		},
	};
}

function normalizeAppendEntry(customTypeOrEntry, maybeData) {
	if (customTypeOrEntry && typeof customTypeOrEntry === "object" && !Array.isArray(customTypeOrEntry)) {
		const customType = messageRendererCustomType(customTypeOrEntry.customType ?? customTypeOrEntry.type ?? customTypeOrEntry.name);
		if (!customType) {
			console.warn("pi.appendEntry expected a customType; skipping.");
			return null;
		}
		return { customType, data: customTypeOrEntry.data };
	}
	const customType = messageRendererCustomType(customTypeOrEntry);
	if (!customType) {
		console.warn("pi.appendEntry expected a customType; skipping.");
		return null;
	}
	return { customType, data: maybeData };
}

function normalizeSetLabel(entryIdOrOptions, maybeLabel) {
	if (entryIdOrOptions && typeof entryIdOrOptions === "object") {
		return {
			entryId: String(entryIdOrOptions.entryId ?? entryIdOrOptions.id ?? entryIdOrOptions.targetId ?? ""),
			label: entryIdOrOptions.label == null ? "" : String(entryIdOrOptions.label),
		};
	}
	return {
		entryId: String(entryIdOrOptions ?? ""),
		label: maybeLabel == null ? "" : String(maybeLabel),
	};
}

function normalizeAutocompleteItem(item) {
	if (typeof item === "string") return { value: item, label: item, description: "" };
	if (!item || typeof item !== "object") return null;
	const value = item.value ?? item.insertText ?? item.text ?? item.label;
	if (value == null) return null;
	const text = String(value);
	return {
		value: text,
		label: item.label == null ? text : String(item.label),
		description: item.description == null ? "" : String(item.description),
	};
}

function normalizeAutocompleteSuggestions(result) {
	if (result == null) return null;
	if (Array.isArray(result)) result = { items: result };
	if (!result || typeof result !== "object") return null;
	const items = (Array.isArray(result.items) ? result.items : [])
		.map(normalizeAutocompleteItem)
		.filter(Boolean);
	if (items.length === 0) return null;
	return {
		items,
		prefix: result.prefix == null ? "" : String(result.prefix),
	};
}

function normalizeAutocompleteApplyResult(result) {
	if (result == null) return null;
	if (typeof result === "string") {
		const lines = result.split("\n");
		const cursorLine = Math.max(0, lines.length - 1);
		const cursorCol = String(lines[cursorLine] ?? "").length;
		return { lines, cursorLine, cursorCol, input: result, cursor: result.length };
	}
	if (!result || typeof result !== "object") return null;
	const lines = Array.isArray(result.lines) ? result.lines.map((line) => String(line ?? "")) : String(result.input ?? "").split("\n");
	const cursorLine = Number.isInteger(result.cursorLine) ? result.cursorLine : Math.max(0, lines.length - 1);
	const cursorCol = Number.isInteger(result.cursorCol) ? result.cursorCol : String(lines[cursorLine] ?? "").length;
	const input = result.input == null ? lines.join("\n") : String(result.input);
	const cursor = Number.isInteger(result.cursor) ? result.cursor : input.length;
	return { lines, cursorLine, cursorCol, input, cursor };
}

const baseAutocompleteProvider = {
	getSuggestions() { return null; },
	applyCompletion(lines, cursorLine, cursorCol, item, prefix) {
		const nextLines = Array.isArray(lines) ? lines.map((line) => String(line ?? "")) : [""];
		const line = String(nextLines[cursorLine] ?? "");
		const typedPrefix = String(prefix ?? "");
		const value = String(item?.value ?? item?.label ?? "");
		const start = Math.max(0, cursorCol - typedPrefix.length);
		nextLines[cursorLine] = line.slice(0, start) + value + line.slice(cursorCol);
		return { lines: nextLines, cursorLine, cursorCol: start + value.length };
	},
	shouldTriggerFileCompletion() { return true; },
};

const api = {
	registerTool(definition) {
		if (!definition?.name) throw new Error("Extension tool is missing a name");
		tools.set(definition.name, definition);
	},
	registerCommand(name, info = {}) {
		if (!name) throw new Error("Extension command is missing a name");
		let description = "";
		let handler;
		if (typeof info === "string") {
			description = info;
		} else if (typeof info === "function") {
			handler = info;
		} else {
			description = info.description ?? "";
			handler = info.handler ?? info.execute ?? info.run;
		}
		commands.set(name, { name, description, handler: typeof handler === "function" ? handler : undefined });
	},
	registerFlag(name, options = {}) {
		if (!name) throw new Error("Extension flag is missing a name");
		const type = options.type === "string" ? "string" : "boolean";
		flags.set(name, { name, description: options.description ?? "", type, default: options.default });
		if (!(name in flagValues) && options.default !== undefined) {
			flagValues[name] = options.default;
		}
	},
	getFlag(name) {
		if (!flags.has(name)) return undefined;
		return flagValues[name];
	},
	on(event, handler) {
		const key = String(event ?? "").trim();
		if (!key || typeof handler !== "function") return () => {};
		const list = eventHandlers.get(key) ?? [];
		list.push(handler);
		eventHandlers.set(key, list);
		return () => eventHandlers.set(key, (eventHandlers.get(key) ?? []).filter((item) => item !== handler));
	},
	onShutdown(handler) {
		if (typeof handler === "function") shutdownHandlers.push(handler);
	},
	registerProvider(apiOrProvider, maybeProvider) {
		const provider = normalizeProviderDefinition(apiOrProvider, maybeProvider);
		if (!provider) return;
		providers.set(provider.key, provider);
		if (provider.hasHandler) providerHandlers.set(provider.api, provider);
		notifyProviderRegistered(provider);
	},
	unregisterProvider(apiOrProvider, maybeProvider) {
		const key = providerKeyName(apiOrProvider, maybeProvider);
		if (!key) {
			console.warn("pi.unregisterProvider expected an api/name; skipping.");
			return;
		}
		const provider = providers.get(key) ?? providerHandlers.get(key);
		providers.delete(provider?.key ?? key);
		if (provider?.hasHandler && providerHandlers.get(provider.api) === provider) {
			providerHandlers.delete(provider.api);
		}
		notifyProviderUnregistered(provider ?? key);
	},
	registerMessageRenderer(customType, handler) {
		const renderer = normalizeMessageRenderer(customType, handler);
		if (!renderer) return;
		messageRenderers.set(renderer.customType, renderer);
		notifyMessageRendererRegistered(renderer);
	},
	unregisterMessageRenderer(customType) {
		const type = messageRendererCustomType(customType);
		if (type && messageRenderers.delete(type)) notifyMessageRendererUnregistered(type);
	},
	sendMessage(message, options = {}) {
		const payload = normalizeOutgoingCustomMessage(message, options);
		if (!payload) return Promise.resolve(null);
		return sendContextAction("sendMessage", payload).catch((error) => {
			warnContextAction("sendMessage", error);
			return null;
		});
	},
	sendUserMessage(content, options = {}) {
		return sendContextAction("sendUserMessage", normalizeSendUserMessage(content, options)).catch((error) => {
			warnContextAction("sendUserMessage", error);
			return null;
		});
	},
	appendEntry(customTypeOrEntry, data) {
		const payload = normalizeAppendEntry(customTypeOrEntry, data);
		if (!payload) return Promise.resolve(null);
		return sendContextAction("appendEntry", payload).catch((error) => {
			warnContextAction("appendEntry", error);
			return null;
		});
	},
	setSessionName(name) {
		return sendContextAction("setSessionName", { name: String(name ?? "") }).catch((error) => {
			warnContextAction("setSessionName", error);
			return null;
		});
	},
	getSessionName() {
		return sendContextAction("getSessionName").catch((error) => {
			warnContextAction("getSessionName", error);
			return undefined;
		});
	},
	setLabel(entryIdOrOptions, label) {
		const payload = normalizeSetLabel(entryIdOrOptions, label);
		if (!payload.entryId) {
			console.warn("pi.setLabel expected an entry id; skipping.");
			return Promise.resolve(null);
		}
		return sendContextAction("setLabel", payload).catch((error) => {
			warnContextAction("setLabel", error);
			return null;
		});
	},
	addAutocompleteProvider(factory) {
		if (typeof factory !== "function") {
			console.warn("pi.addAutocompleteProvider expected a provider factory; skipping.");
			return;
		}
		try {
			const current = autocompleteProviders.length === 0 ? baseAutocompleteProvider : autocompleteProviders[autocompleteProviders.length - 1];
			let provider = factory(current);
			if (typeof provider === "function") provider = { getSuggestions: provider };
			if (!provider || typeof provider.getSuggestions !== "function") {
				console.warn("pi.addAutocompleteProvider ignored a provider without getSuggestions.");
				return;
			}
			autocompleteProviders.push(provider);
		} catch (error) {
			console.warn("pi.addAutocompleteProvider registration failed: " + (error?.message ?? String(error)));
		}
	},
	registerShortcut(shortcut, options = {}) {
		const key = String(shortcut ?? "").trim();
		if (!key) {
			console.warn("pi.registerShortcut expected a key chord; skipping.");
			return;
		}
		const handler = typeof options === "function" ? options : options?.handler;
		if (typeof handler !== "function") {
			console.warn("pi.registerShortcut expected a handler; skipping " + key + ".");
			return;
		}
		shortcuts.set(key, {
			key,
			description: typeof options === "function" ? "" : String(options?.description ?? ""),
			handler,
		});
		notifyShortcutRegistered(shortcuts.get(key));
	},
	unregisterShortcut(shortcut) {
		const key = String(shortcut ?? "").trim();
		if (key && shortcuts.delete(key)) notifyShortcutUnregistered(key);
	},
	events: {
		on(event, handler) { return api.on(event, handler); },
		emit(event, payload = {}) {
			const key = String(event ?? "").trim();
			if (!key) return payload;
			for (const handler of eventHandlers.get(key) ?? []) {
				try {
					const result = handler(payload, extensionContext(payload));
					if (result && typeof result === "object") Object.assign(payload, result);
				} catch (error) {
					console.warn("pi.events.emit handler failed: " + (error?.message ?? String(error)));
				}
			}
			return payload;
		},
	},
	// ctx.ui requests are routed to the host over the bridge (ui_request ->
	// ui_response). When the host bound no handler (truly headless) the host
	// replies with an error and these reject, so a UI-gated extension fails loudly
	// instead of silently taking the wrong branch. Signatures mirror TS:
	// notify(message, level), select(message, choices[]) -> choice,
	// confirm(message, detail) -> boolean, input(message, options) -> string.
	ui: {
		notify(message, level) { return sendUIRequest("notify", { message: String(message ?? ""), level: level ?? "info" }).catch(() => { process.stderr.write(String(message ?? "") + "\n"); }); },
		select(message, choices) { return sendUIRequest("select", { message: String(message ?? ""), choices: Array.isArray(choices) ? choices : [] }); },
		confirm(message, detail) { return sendUIRequest("confirm", { message: String(message ?? ""), detail: detail == null ? "" : String(detail) }); },
		input(message, options) { return sendUIRequest("input", { message: String(message ?? ""), options: options ?? {} }); },
		setStatus(key, text) {
			if (!hasUIState) return;
			sendUIRequest("setStatus", { key: String(key ?? ""), text: text === undefined ? undefined : String(text) })
				.catch((error) => console.warn("ctx.ui.setStatus failed: " + (error?.message ?? String(error))));
		},
		// Lightweight state mutations (TS ExtensionUIContext). These are fire-and-
		// forget over the bridge: gated on hasUIState so they no-op when the host
		// has no UI (headless/print), exactly like setStatus. The host (interactive
		// TUI or RPC broker) decides which are reflected vs no-ops.
		setWorkingMessage(message) {
			if (!hasUIState) return;
			sendUIRequest("setWorkingMessage", { message: message === undefined ? undefined : String(message) })
				.catch((error) => console.warn("ctx.ui.setWorkingMessage failed: " + (error?.message ?? String(error))));
		},
		setWorkingVisible(visible) {
			if (!hasUIState) return;
			sendUIRequest("setWorkingVisible", { visible: Boolean(visible) })
				.catch((error) => console.warn("ctx.ui.setWorkingVisible failed: " + (error?.message ?? String(error))));
		},
		setWorkingIndicator(options) {
			if (!hasUIState) return;
			const opts = options && typeof options === "object" ? options : undefined;
			const frames = opts && Array.isArray(opts.frames) ? opts.frames.map((f) => String(f)) : undefined;
			const intervalMs = opts && typeof opts.intervalMs === "number" ? opts.intervalMs : undefined;
			sendUIRequest("setWorkingIndicator", { frames, intervalMs })
				.catch((error) => console.warn("ctx.ui.setWorkingIndicator failed: " + (error?.message ?? String(error))));
		},
		setHiddenThinkingLabel(label) {
			if (!hasUIState) return;
			sendUIRequest("setHiddenThinkingLabel", { label: label === undefined ? undefined : String(label) })
				.catch((error) => console.warn("ctx.ui.setHiddenThinkingLabel failed: " + (error?.message ?? String(error))));
		},
		setTitle(title) {
			if (!hasUIState) return;
			sendUIRequest("setTitle", { title: String(title ?? "") })
				.catch((error) => console.warn("ctx.ui.setTitle failed: " + (error?.message ?? String(error))));
		},
		pasteToEditor(text) {
			if (!hasUIState) return;
			sendUIRequest("pasteToEditor", { text: String(text ?? "") })
				.catch((error) => console.warn("ctx.ui.pasteToEditor failed: " + (error?.message ?? String(error))));
		},
		setEditorText(text) {
			if (!hasUIState) return;
			sendUIRequest("setEditorText", { text: String(text ?? "") })
				.catch((error) => console.warn("ctx.ui.setEditorText failed: " + (error?.message ?? String(error))));
		},
		// getEditorText is synchronous in TS (returns a string). The out-of-process
		// Go bridge cannot block on the host, so it resolves a Promise<string>
		// (documented divergence in TS_COMPATIBILITY.md). Headless resolves "" with
		// no round trip, matching RPC mode's synchronous "".
		getEditorText() {
			if (!hasUIState) return Promise.resolve("");
			return sendUIRequest("getEditorText", {})
				.then((value) => (typeof value === "string" ? value : String(value ?? "")))
				.catch(() => "");
		},
		// editor(title, prefill) shows a multi-line editor and resolves the result
		// (undefined when cancelled). Headless resolves undefined without a host
		// round trip.
		editor(title, prefill) {
			if (!hasUIState) return Promise.resolve(undefined);
			return sendUIRequest("editor", { title: String(title ?? ""), prefill: prefill === undefined ? undefined : String(prefill) })
				.then((value) => (value == null ? undefined : String(value)))
				.catch(() => undefined);
		},
		// Raw per-keystroke terminal input is not forwarded across the Go bridge:
		// the host runs the TUI in-process, and shuttling every keystroke to the
		// subprocess with synchronous consume/rewrite semantics is impractical.
		// Mirrors RPC mode — registers nothing and returns a no-op unsubscribe.
		onTerminalInput(handler) {
			if (typeof handler !== "function") return () => {};
			console.warn("ctx.ui.onTerminalInput is not supported in the Go bridge; the handler will not receive terminal input.");
			return () => {};
		},
		// setWidget: plain string[] (or undefined to remove) widgets placed above or
		// below the editor, mirroring the subset TS rpc-mode.ts forwards. Component
		// factories are unsupported in the Go bridge (no live TUI component model in
		// the subprocess) — warn and skip, leaving any prior widget for that key.
		setWidget(key, content, options) {
			if (!hasUIState) return;
			if (typeof content === "function") {
				console.warn("ctx.ui.setWidget component factories are not supported in the Go bridge; pass a string[] instead.");
				return;
			}
			const lines = content === undefined ? undefined : (Array.isArray(content) ? content.map((line) => String(line)) : [String(content)]);
			const placement = options && options.placement === "belowEditor" ? "belowEditor" : "aboveEditor";
			sendUIRequest("setWidget", { key: String(key ?? ""), lines, placement })
				.catch((error) => console.warn("ctx.ui.setWidget failed: " + (error?.message ?? String(error))));
		},
		// Custom footer/header/editor components require a live TUI component model
		// the Go bridge does not expose; they degrade to a documented warn-and-no-op.
		setFooter() {
			console.warn("ctx.ui.setFooter is not supported in the Go bridge; custom footer components are unavailable.");
		},
		setHeader() {
			console.warn("ctx.ui.setHeader is not supported in the Go bridge; custom header components are unavailable.");
		},
		setEditorComponent() {
			console.warn("ctx.ui.setEditorComponent is not supported in the Go bridge; custom editor components are unavailable.");
		},
		getEditorComponent() {
			return undefined;
		},
		custom() {
			console.warn("pi.ui.custom is not supported in the Go bridge; skipping (custom overlays are unavailable).");
			return Promise.resolve(undefined);
		},
	},
};

function filterModels(models, search) {
	const list = Array.isArray(models) ? models : [];
	const q = String(search ?? "").trim().toLowerCase();
	if (!q) return list;
	return list.filter((model) => {
		const provider = String(model?.provider ?? "").toLowerCase();
		const id = String(model?.id ?? "").toLowerCase();
		const name = String(model?.name ?? "").toLowerCase();
		return provider.includes(q) || id.includes(q) || name.includes(q) || (provider + "/" + id).includes(q);
	});
}

function findModel(models, provider, modelId) {
	return (Array.isArray(models) ? models : []).find((model) => model?.provider === provider && model?.id === modelId);
}

function branchFromEntries(entries, fromId, fallbackBranch) {
	const list = Array.isArray(entries) ? entries : [];
	if (!fromId) return Array.isArray(fallbackBranch) ? fallbackBranch : [];
	const byId = new Map();
	for (const entry of list) {
		if (entry?.id) byId.set(entry.id, entry);
	}
	const path = [];
	let current = byId.get(fromId);
	while (current) {
		path.unshift(current);
		const parentId = current.parentId;
		if (!parentId) break;
		current = byId.get(parentId);
	}
	return path;
}

function extensionContext(payload = {}, snapshot = {}) {
	const branch = snapshot.branchEntries ?? payload.branchEntries ?? payload.BranchEntries ?? payload.entries ?? payload.Entries ?? [];
	const entries = snapshot.entries ?? payload.entries ?? payload.Entries ?? branch;
	const models = Array.isArray(snapshot.models) ? snapshot.models : [];
	const availableModels = Array.isArray(snapshot.availableModels) ? snapshot.availableModels : models;
	const systemPrompt = snapshot.systemPrompt ?? payload.systemPrompt ?? payload.SystemPrompt ?? "";
	const model = snapshot.model ?? payload.model ?? payload.Model ?? null;
	return {
		cwd: snapshot.cwd ?? process.cwd(),
		mode: snapshot.mode ?? "print",
		hasUI: snapshot.hasUI ?? hasUIState,
		ui: api.ui,
		model,
		modelRegistry: {
			list(search) { return filterModels(models, search); },
			getAll() { return models; },
			getAvailable() { return availableModels; },
			get(provider, modelId) { return findModel(models, provider, modelId); },
			find(provider, modelId) { return findModel(models, provider, modelId); },
			hasConfiguredAuth(candidate) {
				if (!candidate) return false;
				return Boolean(findModel(availableModels, candidate.provider, candidate.id));
			},
			getApiKeyAndHeaders(candidate) {
				return sendContextAction("getApiKeyAndHeaders", { model: candidate ?? null });
			},
		},
		isIdle() { return snapshot.isIdle !== false; },
		signal: payload.signal ?? payload.Signal,
		abort() {
			sendContextAction("abort").catch((error) => warnContextAction("abort", error));
		},
		hasPendingMessages() { return snapshot.hasPendingMessages === true; },
		shutdown() {
			sendContextAction("shutdown").catch((error) => warnContextAction("shutdown", error));
		},
		getContextUsage() { return snapshot.contextUsage ?? undefined; },
		compact(options = {}) {
			sendContextAction("compact", { customInstructions: options?.customInstructions ?? "" })
				.then((result) => {
					if (typeof options?.onComplete === "function") options.onComplete(result);
				})
				.catch((error) => {
					if (typeof options?.onError === "function") options.onError(error);
					else warnContextAction("compact", error);
				});
		},
		getSystemPrompt() { return String(systemPrompt ?? ""); },
		// Command-context methods (TS ExtensionCommandContext). Serializable options
		// only — the withSession/setup callbacks cannot cross the process boundary,
		// so they are dropped. navigateTree/reload/waitForIdle are host-backed;
		// newSession/fork/switchSession/getSystemPromptOptions reject with a clear
		// "not supported by this host" message (see EXTENSIONS_DESIGN.md).
		getSystemPromptOptions() { return sendContextAction("getSystemPromptOptions"); },
		waitForIdle() { return sendContextAction("waitForIdle"); },
		newSession(options = {}) {
			return sendContextAction("newSession", { parentSession: options?.parentSession ?? "" });
		},
		fork(entryId, options = {}) {
			return sendContextAction("fork", { entryId: String(entryId ?? ""), position: options?.position ?? "at" });
		},
		navigateTree(targetId, options = {}) {
			return sendContextAction("navigateTree", {
				targetId: String(targetId ?? ""),
				summarize: options?.summarize === true,
				customInstructions: options?.customInstructions ?? "",
				replaceInstructions: options?.replaceInstructions === true,
				label: options?.label ?? "",
			});
		},
		switchSession(sessionPath, options = {}) {
			return sendContextAction("switchSession", { sessionPath: String(sessionPath ?? "") });
		},
		reload() { return sendContextAction("reload"); },
		sessionManager: {
			getEntries() { return Array.isArray(entries) ? entries : []; },
			getBranch(fromId) { return branchFromEntries(entries, fromId, branch); },
			getLeafId() { return snapshot.leafId ?? ""; },
			getHeader() { return { id: snapshot.sessionId ?? "", path: snapshot.sessionFile ?? "", cwd: snapshot.cwd ?? process.cwd() }; },
		},
	};
}

try {
	if (!extensionPath) throw new Error("extension path is required");
	const mod = await import(pathToFileURL(extensionPath).href);
	const factory = mod.default ?? mod;
	if (typeof factory !== "function") throw new Error("extension default export must be a function");
	await factory(api);
	write({
		type: "ready",
		tools: Array.from(tools.values()).map((tool) => ({
			name: tool.name,
			label: tool.label ?? tool.name,
			description: tool.description ?? "",
			parameters: tool.parameters ?? { type: "object", properties: {} },
		})),
		commands: Array.from(commands.values()).map((command) => ({ name: command.name, description: command.description ?? "" })),
		shortcuts: Array.from(shortcuts.values()).map((shortcut) => ({ key: shortcut.key, description: shortcut.description ?? "" })),
		autocompleteProviders: autocompleteProviders.length,
		providers: Array.from(providers.values()).map(providerMetadata),
		messageRenderers: Array.from(messageRenderers.values()).map(messageRendererMetadata),
		flags: Array.from(flags.values()),
		events: Array.from(eventHandlers.keys()),
	});
	bridgeReady = true;
} catch (error) {
	write({ type: "error", error: error?.stack ?? error?.message ?? String(error) });
	process.exit(1);
}

const rl = createInterface({ input: process.stdin, crlfDelay: Infinity });
rl.on("line", async (line) => {
	let request;
		try {
			request = JSON.parse(line);
			if (request.type === "cancel_request") {
				const controller = autocompleteControllers.get(Number(request.id));
				if (controller) {
					controller.abort();
					autocompleteControllers.delete(Number(request.id));
				}
				const providerController = providerControllers.get(Number(request.id));
				if (providerController) {
					providerController.abort();
					providerControllers.delete(Number(request.id));
				}
				return;
			}
			if (request.type === "set_has_ui") {
				hasUIState = request.value === true;
				return;
			}
			if (request.type === "ui_response") {
				const pending = uiPending.get(request.uiId);
				if (pending) {
					uiPending.delete(request.uiId);
					if (request.ok) pending.resolve(request.result);
					else pending.reject(new Error(request.error || "ui request failed"));
				}
				return;
			}
			if (request.type === "context_action_response") {
				const pending = contextActionPending.get(request.actionId);
				if (pending) {
					contextActionPending.delete(request.actionId);
					if (request.ok) pending.resolve(request.result);
					else pending.reject(new Error(request.error || "context action failed"));
				}
				return;
			}
			if (request.type === "execute_tool") {
				const tool = tools.get(request.toolName);
				if (!tool) throw new Error("unknown extension tool: " + request.toolName);
				const result = await tool.execute(String(request.id ?? ""), request.params ?? {}, undefined, undefined, extensionContext({}, request.context ?? {}));
				write({ id: request.id, ok: true, result: normalizeToolResult(result) });
				return;
		}
			if (request.type === "execute_command") {
				const command = commands.get(request.commandName);
				if (!command) throw new Error("unknown extension command: " + request.commandName);
				if (typeof command.handler !== "function") throw new Error("extension command has no handler: " + request.commandName);
				const result = await command.handler(String(request.args ?? ""), extensionContext({}, request.context ?? {}));
				write({ id: request.id, ok: true, result: result ?? null });
				return;
			}
			if (request.type === "execute_shortcut") {
				const shortcut = shortcuts.get(String(request.key ?? ""));
				if (!shortcut) throw new Error("unknown extension shortcut: " + request.key);
				if (typeof shortcut.handler !== "function") throw new Error("extension shortcut has no handler: " + request.key);
				await shortcut.handler(extensionContext({}, request.context ?? {}));
				write({ id: request.id, ok: true, result: null });
				return;
			}
			if (request.type === "provider_call") {
				const provider = providerHandlers.get(String(request.api ?? "")) ?? providers.get(String(request.api ?? ""));
				if (!provider) throw new Error("unknown extension provider: " + request.api);
				const handler = providerHandlerFor(provider, request);
				if (typeof handler !== "function") throw new Error("extension provider has no handler: " + request.api);
				const controller = new AbortController();
				providerControllers.set(Number(request.id), controller);
				try {
					const result = await handler(request.request ?? {}, extensionContext({}, request.context ?? {}), {
						signal: controller.signal,
						stream: request.stream === true,
						simple: request.simple === true,
						method: request.method ?? "",
					});
					await streamProviderResult(request.id, result, request.stream === true);
				} finally {
					providerControllers.delete(Number(request.id));
				}
				return;
			}
			if (request.type === "render_message") {
				const renderer = messageRenderers.get(String(request.customType ?? ""));
				if (!renderer) throw new Error("unknown extension message renderer: " + request.customType);
				const payload = request.request ?? {};
				const result = await renderer.handler(
					{
						customType: renderer.customType,
						content: payload.content,
						display: payload.display !== false,
						details: payload.details,
						timestamp: payload.timestamp ?? 0,
					},
					{
						expanded: payload.expanded === true,
						width: Number.isInteger(payload.width) ? payload.width : 0,
					},
					messageRendererTheme
				);
				write({ id: request.id, ok: true, result: await normalizeMessageRendererResult(result, payload.width) });
				return;
			}
			if (request.type === "autocomplete") {
				const provider = autocompleteProviders[autocompleteProviders.length - 1];
				if (!provider || typeof provider.getSuggestions !== "function") {
					write({ id: request.id, ok: true, result: null });
					return;
				}
				const payload = request.request ?? {};
				const lines = Array.isArray(payload.lines) ? payload.lines.map((line) => String(line ?? "")) : String(payload.input ?? "").split("\n");
				const cursorLine = Number.isInteger(payload.cursorLine) ? payload.cursorLine : Math.max(0, lines.length - 1);
				const cursorCol = Number.isInteger(payload.cursorCol) ? payload.cursorCol : String(lines[cursorLine] ?? "").length;
				const controller = new AbortController();
				autocompleteControllers.set(Number(request.id), controller);
				try {
					const result = await provider.getSuggestions(lines, cursorLine, cursorCol, { signal: controller.signal, force: payload.force === true });
					write({ id: request.id, ok: true, result: normalizeAutocompleteSuggestions(result) });
				} finally {
					autocompleteControllers.delete(Number(request.id));
				}
				return;
			}
			if (request.type === "autocomplete_apply") {
				const provider = autocompleteProviders[autocompleteProviders.length - 1];
				if (!provider) {
					write({ id: request.id, ok: true, result: null });
					return;
				}
				const payload = request.request ?? {};
				const lines = Array.isArray(payload.lines) ? payload.lines.map((line) => String(line ?? "")) : String(payload.input ?? "").split("\n");
				const cursorLine = Number.isInteger(payload.cursorLine) ? payload.cursorLine : Math.max(0, lines.length - 1);
				const cursorCol = Number.isInteger(payload.cursorCol) ? payload.cursorCol : String(lines[cursorLine] ?? "").length;
				const apply = typeof provider.applyCompletion === "function" ? provider.applyCompletion.bind(provider) : baseAutocompleteProvider.applyCompletion;
				const result = await apply(lines, cursorLine, cursorCol, payload.item ?? {}, payload.prefix ?? "");
				write({ id: request.id, ok: true, result: normalizeAutocompleteApplyResult(result) });
				return;
			}
			if (request.type === "emit") {
				const payload = request.payload ?? {};
				for (const handler of eventHandlers.get(request.event) ?? []) {
					const result = await handler(payload, extensionContext(payload, request.context ?? {}));
					if (result && typeof result === "object") Object.assign(payload, result);
				}
				delete payload.signal;
				delete payload.Signal;
				write({ id: request.id, ok: true, result: payload });
				return;
			}
		if (request.type === "shutdown") {
			for (let i = shutdownHandlers.length - 1; i >= 0; i--) {
				await shutdownHandlers[i](extensionContext({}, request.context ?? {}));
			}
			write({ id: request.id, ok: true, result: null });
			process.exit(0);
			}
			throw new Error("unknown extension request: " + request.type);
		} catch (error) {
		write({ id: request?.id ?? 0, ok: false, error: error?.stack ?? error?.message ?? String(error) });
	}
});
`
