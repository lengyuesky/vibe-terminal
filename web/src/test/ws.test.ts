import { describe, expect, it } from 'vitest';
import { encodeStdin } from '../ws';

describe('encodeStdin', () => {
  it('creates a protocol envelope', () => {
    expect(encodeStdin('sess-1', 'ls\n')).toEqual(JSON.stringify({
      type: 'stdin',
      session_id: 'sess-1',
      payload: { session_id: 'sess-1', data: 'ls\n' },
    }));
  });
});
