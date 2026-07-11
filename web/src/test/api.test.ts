import { afterEach, describe, expect, it, vi } from 'vitest';
import { APIError, login, me, verifyTwoFactor } from '../api';

function jsonResponse(status: number, body: unknown, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('登录 API', () => {
  it('兼容 200 用户响应并发送完整登录请求', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { id: 'user-1', username: 'admin' }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(login('admin', 'secret')).resolves.toEqual({
      status: 'authenticated',
      user: { id: 'user-1', username: 'admin' },
    });
    expect(fetchMock).toHaveBeenCalledWith('/api/login', {
      method: 'POST',
      body: JSON.stringify({ username: 'admin', password: 'secret' }),
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
    });
  });

  it('解析 202 二因素挑战', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(202, {
          two_factor_required: true,
          challenge_token: 'challenge-1',
          expires_in: 300,
        })
      )
    );

    await expect(login('admin', 'secret')).resolves.toEqual({
      status: 'two_factor_required',
      challengeToken: 'challenge-1',
      expiresIn: 300,
    });
  });

  it('拒绝字段缺失或类型错误的 202 挑战', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(202, { two_factor_required: true, challenge_token: '', expires_in: '300' })
      )
    );

    await expect(login('admin', 'secret')).rejects.toMatchObject({
      name: 'APIError',
      status: 202,
      code: 'invalid_response',
    });
  });

  it('提交第二因素并返回服务端用户', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { id: 'user-1', username: 'admin' }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(verifyTwoFactor('challenge-1', '123456')).resolves.toEqual({
      id: 'user-1',
      username: 'admin',
    });
    expect(fetchMock).toHaveBeenCalledWith('/api/login/2fa', {
      method: 'POST',
      body: JSON.stringify({ challenge_token: 'challenge-1', code: '123456' }),
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
    });
  });
});

describe('统一 API 错误', () => {
  it('解析结构化错误和有限非负 Retry-After', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(
          429,
          { code: 'too_many_attempts', message: 'too many login attempts' },
          { 'Retry-After': '12.5' }
        )
      )
    );

    const error = await login('admin', 'wrong').catch((caught) => caught);
    expect(error).toBeInstanceOf(APIError);
    expect(error).toMatchObject({
      status: 429,
      code: 'too_many_attempts',
      message: 'too many login attempts',
      retryAfter: 12.5,
    });
  });

  it.each(['-1', 'Infinity', 'NaN', ''])('忽略无效 Retry-After：%s', async (retryAfter) => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(429, { code: 'limited', message: 'wait' }, { 'Retry-After': retryAfter })
      )
    );

    const error = (await login('admin', 'wrong').catch((caught) => caught)) as APIError;
    expect(error.retryAfter).toBeUndefined();
  });

  it('将非 JSON 错误转换为明确 APIError', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('gateway down', { status: 502 })));

    await expect(me()).rejects.toMatchObject({
      name: 'APIError',
      status: 502,
      code: 'http_error',
      message: 'gateway down',
    });
  });

  it('拒绝成功状态下的空响应或非 JSON 响应', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('', { status: 200 })));
    await expect(me()).rejects.toMatchObject({ code: 'invalid_response' });

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('not-json', { status: 200 })));
    await expect(me()).rejects.toMatchObject({ code: 'invalid_response' });
  });
});
