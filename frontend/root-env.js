import fs from "node:fs";
import path from "node:path";
export var defaultFrontendEnvKeys = [
    "VITE_DEFAULT_API_BASE_URL",
    "VITE_DEFAULT_BEARER_TOKEN",
];
var rootEnvFilenames = [".env.personal", ".env"];
export function resolveRootEnvFile(rootDir) {
    for (var _i = 0, rootEnvFilenames_1 = rootEnvFilenames; _i < rootEnvFilenames_1.length; _i++) {
        var filename = rootEnvFilenames_1[_i];
        var filePath = path.join(rootDir, filename);
        if (fs.existsSync(filePath)) {
            return filePath;
        }
    }
    return null;
}
export function loadRootDefaultEnv(rootDir) {
    var rootEnvFile = resolveRootEnvFile(rootDir);
    if (!rootEnvFile) {
        return {};
    }
    return parseRootEnv(fs.readFileSync(rootEnvFile, "utf8"));
}
export function applyRootDefaultEnv(rootDir, env) {
    if (env === void 0) { env = process.env; }
    var defaults = loadRootDefaultEnv(rootDir);
    for (var _i = 0, defaultFrontendEnvKeys_1 = defaultFrontendEnvKeys; _i < defaultFrontendEnvKeys_1.length; _i++) {
        var key = defaultFrontendEnvKeys_1[_i];
        var value = defaults[key];
        if (value !== undefined && env[key] === undefined) {
            env[key] = value;
        }
    }
    return defaults;
}
function parseRootEnv(content) {
    var values = {};
    for (var _i = 0, _a = content.split(/\r?\n/); _i < _a.length; _i++) {
        var line = _a[_i];
        var match = line.match(/^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)\s*$/);
        if (!match) {
            continue;
        }
        var key = match[1];
        if (!isDefaultFrontendEnvKey(key)) {
            continue;
        }
        values[key] = parseRootEnvValue(match[2]);
    }
    return values;
}
function isDefaultFrontendEnvKey(value) {
    return defaultFrontendEnvKeys.includes(value);
}
function parseRootEnvValue(value) {
    var normalizedValue = stripInlineComment(value).trim();
    if (normalizedValue.length >= 2 &&
        ((normalizedValue.startsWith('"') && normalizedValue.endsWith('"')) ||
            (normalizedValue.startsWith("'") && normalizedValue.endsWith("'")))) {
        return normalizedValue.slice(1, -1);
    }
    return normalizedValue;
}
function stripInlineComment(value) {
    var activeQuote = null;
    for (var i = 0; i < value.length; i += 1) {
        var char = value[i];
        if (activeQuote) {
            if (char === activeQuote) {
                activeQuote = null;
            }
            continue;
        }
        if (char === '"' || char === "'") {
            activeQuote = char;
            continue;
        }
        if (char === "#") {
            return value.slice(0, i).trimEnd();
        }
    }
    return value;
}
