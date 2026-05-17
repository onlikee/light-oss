export declare const defaultFrontendEnvKeys: readonly ["VITE_DEFAULT_API_BASE_URL", "VITE_DEFAULT_BEARER_TOKEN"];
type DefaultFrontendEnvKey = (typeof defaultFrontendEnvKeys)[number];
type DefaultFrontendEnv = Partial<Record<DefaultFrontendEnvKey, string>>;
export declare function resolveRootEnvFile(rootDir: string): string | null;
export declare function loadRootDefaultEnv(rootDir: string): DefaultFrontendEnv;
export declare function applyRootDefaultEnv(rootDir: string, env?: NodeJS.ProcessEnv): DefaultFrontendEnv;
export {};
