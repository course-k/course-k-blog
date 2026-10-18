import { defineConfig } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
export default defineConfig({
  plugins: [pluginReact()],
  source: { entry: { index: "./ui-tests/fixture.tsx" } },
  output: {
    distPath: { root: "./dist/ui-test" },
    inlineScripts: true,
    inlineStyles: true,
  },
  performance: { chunkSplit: { strategy: "all-in-one" } },
  html: { inject: "body", title: "UI検証専用・架空データ" },
});
