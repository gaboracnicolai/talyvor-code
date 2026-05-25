"use strict";
// Configuration adapter around vscode.workspace.getConfiguration.
// The Lens/Track clients only see plain objects so they stay
// testable without the VS Code API.
var __createBinding = (this && this.__createBinding) || (Object.create ? (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    var desc = Object.getOwnPropertyDescriptor(m, k);
    if (!desc || ("get" in desc ? !m.__esModule : desc.writable || desc.configurable)) {
      desc = { enumerable: true, get: function() { return m[k]; } };
    }
    Object.defineProperty(o, k2, desc);
}) : (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    o[k2] = m[k];
}));
var __setModuleDefault = (this && this.__setModuleDefault) || (Object.create ? (function(o, v) {
    Object.defineProperty(o, "default", { enumerable: true, value: v });
}) : function(o, v) {
    o["default"] = v;
});
var __importStar = (this && this.__importStar) || (function () {
    var ownKeys = function(o) {
        ownKeys = Object.getOwnPropertyNames || function (o) {
            var ar = [];
            for (var k in o) if (Object.prototype.hasOwnProperty.call(o, k)) ar[ar.length] = k;
            return ar;
        };
        return ownKeys(o);
    };
    return function (mod) {
        if (mod && mod.__esModule) return mod;
        var result = {};
        if (mod != null) for (var k = ownKeys(mod), i = 0; i < k.length; i++) if (k[i] !== "default") __createBinding(result, mod, k[i]);
        __setModuleDefault(result, mod);
        return result;
    };
})();
Object.defineProperty(exports, "__esModule", { value: true });
exports.TalyvorConfig = void 0;
const vscode = __importStar(require("vscode"));
const SECTION = "talyvor";
class TalyvorConfig {
    static getLensConfig() {
        const cfg = vscode.workspace.getConfiguration(SECTION);
        return {
            url: cfg.get("lensUrl", ""),
            apiKey: cfg.get("lensApiKey", ""),
            workspaceId: cfg.get("workspaceId", ""),
            activeIssue: cfg.get("activeIssue", ""),
            model: cfg.get("model", "claude-haiku-4-6"),
            trackUrl: cfg.get("trackUrl", ""),
            trackApiKey: cfg.get("trackApiKey", ""),
            enableCompletions: cfg.get("enableCompletions", true),
        };
    }
    // setActiveIssue persists to the workspace-scoped config so the
    // selection follows the project. Global scope would leak the
    // active issue across unrelated repos.
    static async setActiveIssue(issue) {
        const cfg = vscode.workspace.getConfiguration(SECTION);
        await cfg.update("activeIssue", issue, vscode.ConfigurationTarget.Workspace);
    }
    static isConfigured() {
        const c = this.getLensConfig();
        return !!c.url && !!c.apiKey;
    }
    // validate returns a list of human-readable issues so the
    // welcome message + test-connection command can show the user
    // exactly what's missing.
    static validate() {
        const c = this.getLensConfig();
        const out = [];
        if (!c.url)
            out.push("Lens URL is required (talyvor.lensUrl)");
        if (!c.apiKey)
            out.push("Lens API key is required (talyvor.lensApiKey)");
        if (!c.workspaceId)
            out.push("Workspace ID is required (talyvor.workspaceId)");
        return out;
    }
}
exports.TalyvorConfig = TalyvorConfig;
//# sourceMappingURL=config.js.map