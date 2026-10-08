import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

// Separate from vite.config.ts on purpose: the Module Federation plugin there
// targets a real build/dev server and has no useful role in a unit-test
// process, so tests get their own minimal Vite config (React + jsdom) rather
// than trying to make MF cooperate with vitest.
export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    globals: true,
  },
});
