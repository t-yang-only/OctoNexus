import path from 'node:path';
import { readFileSync } from 'node:fs';
import babel from '@rolldown/plugin-babel';
import react, { reactCompilerPreset } from '@vitejs/plugin-react';
import { defineConfig } from 'vite';
import { compression, defineAlgorithm } from 'vite-plugin-compression2';

// 前端版本号在构建时注入：唯一源头是 main.go 的 `// Version`（项目自己定的规矩）。
// 不注入的话 import.meta.env.VITE_APP_VERSION 是 undefined，面板会把版本显示成 "unknown"，
// 并因此**误报"前端与后端版本不一致"**（实测踩过）。
function appVersion(): string {
  try {
    const main = readFileSync(new URL('../main.go', import.meta.url), 'utf8');
    const matched = main.match(/\/\/\s*Version\s+(\S+)/);
    return matched ? matched[1] : 'unknown';
  } catch {
    return 'unknown';
  }
}

export default defineConfig({
  define: {
    'import.meta.env.VITE_APP_VERSION': JSON.stringify(process.env.VITE_APP_VERSION || appVersion()),
  },
  base: './',
  plugins: [
    react(),
    babel({ presets: [reactCompilerPreset()] }),
    compression({
      algorithms: [defineAlgorithm('gzip', { level: 9 })],
      include: /\.(html|css|js|mjs|json|svg|txt|xml)$/,
      deleteOriginalAssets: true,
    }),
  ],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  build: {
    outDir: path.resolve(import.meta.dirname, '../static/out'),
    emptyOutDir: true,
  },
  server: {
    hmr: process.env.DISABLE_HMR !== 'true',
    watch: process.env.DISABLE_HMR === 'true' ? null : {},
    proxy: {
      '/api': {
        target: process.env.VITE_PROXY_TARGET || 'http://127.0.0.1:3303',
        changeOrigin: false,
      },
    },
  },
});
