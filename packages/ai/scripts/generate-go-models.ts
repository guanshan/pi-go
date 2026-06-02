#!/usr/bin/env node
//
// generate-go-models.ts
//
// Regenerates the Go model catalog (packages/ai/models_generated.go and
// packages/ai/image_models_generated.go) from the upstream TypeScript source
// of truth in badlogic/pi-mono (packages/ai/src/models.generated.ts and
// image-models.generated.ts).
//
// It imports the *committed* runtime objects (MODELS / IMAGE_MODELS) — which
// already have compat / thinkingLevelMap baked in by the upstream
// scripts/generate-models.ts — and reuses the upstream
// getSupportedThinkingLevels(model) helper so the derived Go ThinkingLevels
// list is identical to the TS one. It never hits the network.
//
// Usage:
//   node packages/ai/scripts/generate-go-models.ts [tsSrcDir]
//
//   tsSrcDir   Path to the TS package's src/ directory.
//              Defaults to $PI_TS_AI_SRC, then /root/guanshan/pi/packages/ai/src.
//
// After writing, the files are gofmt-aligned by the caller (the script shells
// out to `gofmt -w`). The `// Code generated ... DO NOT EDIT.` header is kept so
// scripts/check_arch.go excludes the files from line budgets.

import { execFileSync } from "node:child_process";
import { writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { pathToFileURL } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
// Go package root: packages/ai (two levels up from packages/ai/scripts).
const goAiDir = resolve(__dirname, "..");

const DEFAULT_TS_SRC = "/root/guanshan/pi/packages/ai/src";
const tsSrcDir = resolve(process.argv[2] ?? process.env.PI_TS_AI_SRC ?? DEFAULT_TS_SRC);

function srcImport(file: string): string {
	return pathToFileURL(join(tsSrcDir, file)).href;
}

// ---- Go value serializers -------------------------------------------------

function goString(value: string): string {
	// Emit a Go interpreted string literal. Model ids/names are plain ASCII in
	// the catalog; escape the few characters that would break the literal.
	return JSON.stringify(value);
}

function goStringSlice(values: readonly string[]): string {
	return `[]string{${values.map(goString).join(", ")}}`;
}

function goBoolPtr(value: boolean): string {
	return `boolPtr(${value ? "true" : "false"})`;
}

const THINKING_LEVEL_CONST: Record<string, string> = {
	off: "ThinkingOff",
	minimal: "ThinkingMinimal",
	low: "ThinkingLow",
	medium: "ThinkingMedium",
	high: "ThinkingHigh",
	xhigh: "ThinkingXHigh",
};

function goThinkingLevels(levels: readonly string[]): string {
	const consts = levels.map((level) => {
		const c = THINKING_LEVEL_CONST[level];
		if (!c) throw new Error(`unknown thinking level: ${level}`);
		return c;
	});
	return `[]ThinkingLevel{${consts.join(", ")}}`;
}

// Deterministic order for thinkingLevelMap keys so the Go diff is stable.
const THINKING_LEVEL_ORDER = ["off", "minimal", "low", "medium", "high", "xhigh"];

function goThinkingLevelMap(map: Record<string, string | null>): string {
	const keys = Object.keys(map).sort(
		(a, b) => THINKING_LEVEL_ORDER.indexOf(a) - THINKING_LEVEL_ORDER.indexOf(b),
	);
	const entries = keys.map((key) => {
		const value = map[key];
		const goValue = value === null || value === undefined ? "nil" : `strPtr(${goString(value)})`;
		return `${goString(key)}: ${goValue}`;
	});
	return `map[string]*string{${entries.join(", ")}}`;
}

function goCost(cost: { input?: number; output?: number; cacheRead?: number; cacheWrite?: number }): string {
	const num = (n: number | undefined): string => formatNumber(n ?? 0);
	return `ModelCost{Input: ${num(cost.input)}, Output: ${num(cost.output)}, CacheRead: ${num(cost.cacheRead)}, CacheWrite: ${num(cost.cacheWrite)}}`;
}

function goImageCost(cost: { input?: number; output?: number; cacheRead?: number; cacheWrite?: number }): string {
	const num = (n: number | undefined): string => formatNumber(n ?? 0);
	return `Cost{Input: ${num(cost.input)}, Output: ${num(cost.output)}, CacheRead: ${num(cost.cacheRead)}, CacheWrite: ${num(cost.cacheWrite)}}`;
}

function formatNumber(n: number): string {
	// Match Go's float formatting for catalog values: integers stay integers,
	// fractions use the shortest round-trippable decimal (matches %v / strconv).
	if (Number.isInteger(n)) return String(n);
	return String(n);
}

function goStringMap(value: Record<string, string>): string {
	const keys = Object.keys(value).sort();
	const entries = keys.map((key) => `${goString(key)}: ${goString(value[key])}`);
	return `map[string]string{${entries.join(", ")}}`;
}

function goAnyMap(value: Record<string, unknown>): string {
	// Only non-empty routing maps reach here; keys sorted for stability.
	const keys = Object.keys(value).sort();
	const entries = keys.map((key) => `${goString(key)}: ${goAnyValue(value[key])}`);
	return `map[string]any{${entries.join(", ")}}`;
}

function goAnyValue(value: unknown): string {
	if (value === null || value === undefined) return "nil";
	if (typeof value === "string") return goString(value);
	if (typeof value === "boolean") return value ? "true" : "false";
	if (typeof value === "number") return formatNumber(value);
	if (Array.isArray(value)) return `[]any{${value.map(goAnyValue).join(", ")}}`;
	if (typeof value === "object") return goAnyMap(value as Record<string, unknown>);
	throw new Error(`cannot serialize routing value: ${String(value)}`);
}

// Maps a TS compat object to a Go OpenAICompat{...} literal, emitting only the
// fields that are present. Field order follows the Go struct declaration in
// packages/ai/types.go so gofmt produces a stable layout. Keep this in sync
// with the OpenAICompat struct.
function goCompat(compat: Record<string, unknown>): string {
	const fields: string[] = [];
	const boolField = (tsKey: string, goField: string) => {
		const v = compat[tsKey];
		if (typeof v === "boolean") fields.push(`${goField}: ${goBoolPtr(v)}`);
	};
	const stringField = (tsKey: string, goField: string) => {
		const v = compat[tsKey];
		if (typeof v === "string" && v !== "") fields.push(`${goField}: ${goString(v)}`);
	};
	const anyMapField = (tsKey: string, goField: string) => {
		const v = compat[tsKey];
		if (v && typeof v === "object" && Object.keys(v as object).length > 0) {
			fields.push(`${goField}: ${goAnyMap(v as Record<string, unknown>)}`);
		}
	};
	const plainBoolField = (tsKey: string, goField: string) => {
		const v = compat[tsKey];
		if (v === true) fields.push(`${goField}: true`);
	};

	// Order mirrors type OpenAICompat struct.
	boolField("supportsStore", "SupportsStore");
	boolField("supportsDeveloperRole", "SupportsDeveloperRole");
	boolField("supportsReasoningEffort", "SupportsReasoningEffort");
	boolField("supportsUsageInStreaming", "SupportsUsageInStreaming");
	stringField("maxTokensField", "MaxTokensField");
	boolField("requiresToolResultName", "RequiresToolResultName");
	boolField("requiresAssistantAfterToolResult", "RequiresAssistantAfterToolResult");
	boolField("requiresThinkingAsText", "RequiresThinkingAsText");
	boolField("requiresReasoningContentOnAssistantMessages", "RequiresReasoningContentOnAssistantMessages");
	stringField("thinkingFormat", "ThinkingFormat");
	anyMapField("openRouterRouting", "OpenRouterRouting");
	anyMapField("vercelGatewayRouting", "VercelGatewayRouting");
	boolField("zaiToolStream", "ZaiToolStream");
	boolField("supportsStrictMode", "SupportsStrictMode");
	stringField("cacheControlFormat", "CacheControlFormat");
	plainBoolField("sendSessionAffinityHeaders", "SendSessionAffinityHeaders");
	boolField("supportsLongCacheRetention", "SupportsLongCacheRetention");
	boolField("sendSessionIdHeader", "SendSessionIDHeader");
	boolField("supportsEagerToolInputStreaming", "SupportsEagerToolInputStreaming");
	boolField("supportsCacheControlOnTools", "SupportsCacheControlOnTools");
	boolField("supportsTemperature", "SupportsTemperature");
	boolField("forceAdaptiveThinking", "ForceAdaptiveThinking");
	boolField("allowEmptySignature", "AllowEmptySignature");

	// Warn loudly if the catalog grows a compat key the Go struct lacks, so the
	// generator is never silently lossy.
	const known = new Set([
		"supportsStore",
		"supportsDeveloperRole",
		"supportsReasoningEffort",
		"supportsUsageInStreaming",
		"maxTokensField",
		"requiresToolResultName",
		"requiresAssistantAfterToolResult",
		"requiresThinkingAsText",
		"requiresReasoningContentOnAssistantMessages",
		"thinkingFormat",
		"openRouterRouting",
		"vercelGatewayRouting",
		"zaiToolStream",
		"supportsStrictMode",
		"cacheControlFormat",
		"sendSessionAffinityHeaders",
		"supportsLongCacheRetention",
		"sendSessionIdHeader",
		"supportsEagerToolInputStreaming",
		"supportsCacheControlOnTools",
		"supportsTemperature",
		"forceAdaptiveThinking",
		"allowEmptySignature",
	]);
	for (const key of Object.keys(compat)) {
		if (!known.has(key)) {
			throw new Error(
				`compat key '${key}' is not mapped to the Go OpenAICompat struct; add it to packages/ai/types.go and this generator`,
			);
		}
	}

	return `OpenAICompat{${fields.join(", ")}}`;
}

// ---- Emit ------------------------------------------------------------------

interface TsModel {
	id: string;
	name?: string;
	api: string;
	provider: string;
	baseUrl?: string;
	reasoning?: boolean;
	input?: string[];
	thinkingLevelMap?: Record<string, string | null>;
	headers?: Record<string, string>;
	compat?: Record<string, unknown>;
	cost: { input?: number; output?: number; cacheRead?: number; cacheWrite?: number };
	contextWindow?: number;
	maxTokens?: number;
}

function emitModel(model: TsModel, getLevels: (m: TsModel) => string[]): string {
	const lines: string[] = ["\t{"];
	lines.push(`\t\tProvider: ${goString(model.provider)},`);
	lines.push(`\t\tID: ${goString(model.id)},`);
	if (model.name) lines.push(`\t\tName: ${goString(model.name)},`);
	lines.push(`\t\tAPI: ${goString(model.api)},`);
	lines.push(`\t\tBaseURL: ${goString(model.baseUrl ?? "")},`);
	if (model.input && model.input.length > 0) lines.push(`\t\tInput: ${goStringSlice(model.input)},`);
	if (model.reasoning) lines.push(`\t\tReasoning: true,`);
	const levels = getLevels(model);
	if (levels.length > 0) lines.push(`\t\tThinkingLevels: ${goThinkingLevels(levels)},`);
	if (model.thinkingLevelMap && Object.keys(model.thinkingLevelMap).length > 0) {
		lines.push(`\t\tThinkingLevelMap: ${goThinkingLevelMap(model.thinkingLevelMap)},`);
	}
	if (model.contextWindow) lines.push(`\t\tContextWindow: ${model.contextWindow},`);
	if (model.maxTokens) lines.push(`\t\tMaxOutput: ${model.maxTokens},`);
	lines.push(`\t\tCost: ${goCost(model.cost)},`);
	if (model.headers && Object.keys(model.headers).length > 0) {
		lines.push(`\t\tHeaders: ${goStringMap(model.headers)},`);
	}
	if (model.compat && Object.keys(model.compat).length > 0) {
		lines.push(`\t\tCompat: ${goCompat(model.compat)},`);
	}
	lines.push("\t},");
	return lines.join("\n");
}

async function generateTextModels(): Promise<void> {
	const modelsGen = await import(srcImport("models.generated.ts"));
	const modelsMod = await import(srcImport("models.ts"));
	const MODELS = modelsGen.MODELS as Record<string, Record<string, TsModel>>;
	const getSupportedThinkingLevels = modelsMod.getSupportedThinkingLevels as (m: TsModel) => string[];

	const providerIds = Object.keys(MODELS).sort();
	const blocks: string[] = [];
	let count = 0;
	for (const providerId of providerIds) {
		const models = MODELS[providerId];
		const modelIds = Object.keys(models).sort();
		for (const modelId of modelIds) {
			blocks.push(emitModel(models[modelId], getSupportedThinkingLevels));
			count++;
		}
	}

	const out = `package ai

// Code generated from packages/ai/src/models.generated.ts in badlogic/pi-mono; DO NOT EDIT.

var generatedModels = []Model{
${blocks.join("\n")}
}
`;
	const outPath = join(goAiDir, "models_generated.go");
	writeFileSync(outPath, out);
	execFileSync("gofmt", ["-w", outPath]);
	console.log(`Generated ${outPath} (${count} models)`);
}

async function generateImageModels(): Promise<void> {
	const imgGen = await import(srcImport("image-models.generated.ts"));
	const envMod = (await import(srcImport("env-api-keys.ts")).catch(() => null)) as {
		findEnvKeys?: (provider: string) => string[] | undefined;
	} | null;
	const IMAGE_MODELS = imgGen.IMAGE_MODELS as Record<string, Record<string, TsModel & { output?: string[] }>>;

	// The TS image catalog has no envKey; the Go catalog injects the provider's
	// primary env key (mirrors the committed image_models_generated.go).
	const envKeyForProvider = (provider: string): string | undefined => {
		if (envMod && typeof envMod.findEnvKeys === "function") {
			const vars = envMod.findEnvKeys(provider);
			if (vars && vars.length > 0) return vars[0];
		}
		// Fallback table covering the providers that appear in the image catalog.
		const table: Record<string, string> = { openrouter: "OPENROUTER_API_KEY" };
		return table[provider];
	};

	const providerIds = Object.keys(IMAGE_MODELS).sort();
	const blocks: string[] = [];
	let count = 0;
	for (const providerId of providerIds) {
		const models = IMAGE_MODELS[providerId];
		const modelIds = Object.keys(models).sort();
		for (const modelId of modelIds) {
			const model = models[modelId];
			const lines: string[] = ["\t{"];
			lines.push(`\t\tProvider: ${goString(model.provider)},`);
			lines.push(`\t\tID: ${goString(model.id)},`);
			if (model.name) lines.push(`\t\tName: ${goString(model.name)},`);
			lines.push(`\t\tAPI: ${goString(model.api)},`);
			lines.push(`\t\tBaseURL: ${goString(model.baseUrl ?? "")},`);
			const envKey = envKeyForProvider(model.provider);
			if (envKey) lines.push(`\t\tEnvKey: ${goString(envKey)},`);
			if (model.input && model.input.length > 0) lines.push(`\t\tInput: ${goStringSlice(model.input)},`);
			if (model.output && model.output.length > 0) lines.push(`\t\tOutput: ${goStringSlice(model.output)},`);
			lines.push(`\t\tCost: ${goImageCost(model.cost)},`);
			if (model.headers && Object.keys(model.headers).length > 0) {
				lines.push(`\t\tHeaders: ${goStringMap(model.headers)},`);
			}
			lines.push("\t},");
			blocks.push(lines.join("\n"));
			count++;
		}
	}

	const out = `package ai

// Code generated from packages/ai/src/image-models.generated.ts in badlogic/pi-mono; DO NOT EDIT.

func init() {
	RegisterImageModels(generatedImageModels...)
}

var generatedImageModels = []ImagesModel{
${blocks.join("\n")}
}
`;
	const outPath = join(goAiDir, "image_models_generated.go");
	writeFileSync(outPath, out);
	execFileSync("gofmt", ["-w", outPath]);
	console.log(`Generated ${outPath} (${count} image models)`);
}

async function main(): Promise<void> {
	console.log(`TS source: ${tsSrcDir}`);
	await generateTextModels();
	await generateImageModels();
}

main().catch((error) => {
	console.error(error);
	process.exit(1);
});
