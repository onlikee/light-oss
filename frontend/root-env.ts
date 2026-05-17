import fs from "node:fs";
import path from "node:path";

export const defaultFrontendEnvKeys = [
  "VITE_DEFAULT_API_BASE_URL",
  "VITE_DEFAULT_BEARER_TOKEN",
] as const;

type DefaultFrontendEnvKey = (typeof defaultFrontendEnvKeys)[number];
type DefaultFrontendEnv = Partial<Record<DefaultFrontendEnvKey, string>>;

const rootEnvFilenames = [".env.personal", ".env"] as const;

export function resolveRootEnvFile(rootDir: string): string | null {
  for (const filename of rootEnvFilenames) {
    const filePath = path.join(rootDir, filename);
    if (fs.existsSync(filePath)) {
      return filePath;
    }
  }

  return null;
}

export function loadRootDefaultEnv(rootDir: string): DefaultFrontendEnv {
  const rootEnvFile = resolveRootEnvFile(rootDir);
  if (!rootEnvFile) {
    return {};
  }

  return parseRootEnv(fs.readFileSync(rootEnvFile, "utf8"));
}

export function applyRootDefaultEnv(
  rootDir: string,
  env: NodeJS.ProcessEnv = process.env,
): DefaultFrontendEnv {
  const defaults = loadRootDefaultEnv(rootDir);

  for (const key of defaultFrontendEnvKeys) {
    const value = defaults[key];
    if (value !== undefined && env[key] === undefined) {
      env[key] = value;
    }
  }

  return defaults;
}

function parseRootEnv(content: string): DefaultFrontendEnv {
  const values: DefaultFrontendEnv = {};

  for (const line of content.split(/\r?\n/)) {
    const match = line.match(
      /^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)\s*$/,
    );
    if (!match) {
      continue;
    }

    const key = match[1];
    if (!isDefaultFrontendEnvKey(key)) {
      continue;
    }

    values[key] = parseRootEnvValue(match[2]);
  }

  return values;
}

function isDefaultFrontendEnvKey(value: string): value is DefaultFrontendEnvKey {
  return defaultFrontendEnvKeys.includes(value as DefaultFrontendEnvKey);
}

function parseRootEnvValue(value: string): string {
  const normalizedValue = stripInlineComment(value).trim();

  if (
    normalizedValue.length >= 2 &&
    ((normalizedValue.startsWith('"') && normalizedValue.endsWith('"')) ||
      (normalizedValue.startsWith("'") && normalizedValue.endsWith("'")))
  ) {
    return normalizedValue.slice(1, -1);
  }

  return normalizedValue;
}

function stripInlineComment(value: string): string {
  let activeQuote: '"' | "'" | null = null;

  for (let i = 0; i < value.length; i += 1) {
    const char = value[i];

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
