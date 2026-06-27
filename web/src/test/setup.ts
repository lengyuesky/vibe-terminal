import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';

afterEach(() => {
  cleanup();
});

Object.defineProperty(HTMLCanvasElement.prototype, 'getContext', {
  value: () => ({
    createLinearGradient: () => ({ addColorStop: () => undefined }),
    fillRect: () => undefined,
    clearRect: () => undefined,
    getImageData: () => ({ data: [0, 0, 0, 0] }),
    putImageData: () => undefined,
    createImageData: () => [],
    setTransform: () => undefined,
    drawImage: () => undefined,
    save: () => undefined,
    fillText: () => undefined,
    restore: () => undefined,
    beginPath: () => undefined,
    moveTo: () => undefined,
    lineTo: () => undefined,
    closePath: () => undefined,
    stroke: () => undefined,
    translate: () => undefined,
    scale: () => undefined,
    rotate: () => undefined,
    arc: () => undefined,
    fill: () => undefined,
    measureText: () => ({ width: 8 }),
    transform: () => undefined,
    rect: () => undefined,
    clip: () => undefined,
  }),
});
