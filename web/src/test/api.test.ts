import { afterEach, describe, expect, it, vi } from 'vitest';
import { APIError, fetchResponse, login, me, verifyTwoFactor } from '../api';

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
    expect(fetchMock).toHaveBeenCalledOnce();
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe('/api/login');
    expect(init).toMatchObject({
      method: 'POST',
      body: JSON.stringify({ username: 'admin', password: 'secret' }),
      credentials: 'include',
    });
    expect(new Headers(init.headers).get('Content-Type')).toBe('application/json');
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
    expect(fetchMock).toHaveBeenCalledOnce();
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe('/api/login/2fa');
    expect(init).toMatchObject({
      method: 'POST',
      body: JSON.stringify({ challenge_token: 'challenge-1', code: '123456' }),
      credentials: 'include',
    });
    expect(new Headers(init.headers).get('Content-Type')).toBe('application/json');
  });

  it.each([201, 202, 204])('拒绝第二因素意外成功状态 %s', async (status) => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        status === 204
          ? new Response(null, { status })
          : jsonResponse(status, { id: 'user-1', username: 'admin' })
      )
    );

    await expect(verifyTwoFactor('challenge-1', '123456')).rejects.toMatchObject({
      status,
      code: 'invalid_response',
    });
  });

  it('拒绝第二因素 200 空响应和非 JSON 响应', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('', { status: 200 })));
    await expect(verifyTwoFactor('challenge-1', '123456')).rejects.toMatchObject({ code: 'invalid_response' });

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('not-json', { status: 200 })));
    await expect(verifyTwoFactor('challenge-1', '123456')).rejects.toMatchObject({ code: 'invalid_response' });
  });
});

describe('统一请求头', () => {
  it.each([
    { name: '对象', headers: { Accept: 'application/json' }, expected: 'application/json' },
    { name: 'Headers', headers: new Headers({ Accept: 'text/plain' }), expected: 'text/plain' },
    { name: '元组', headers: [['Accept', 'application/problem+json']] as [string, string][], expected: 'application/problem+json' },
  ])('兼容 $name 形式并补默认 Content-Type', async ({ headers, expected }) => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);

    await fetchResponse('/api/example', { headers });
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    const sent = new Headers(init.headers);
    expect(sent.get('Accept')).toBe(expected);
    expect(sent.get('Content-Type')).toBe('application/json');
    expect(init.credentials).toBe('include');
  });

  it('保留调用方显式 Content-Type', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);

    await fetchResponse('/api/upload', { headers: new Headers({ 'Content-Type': 'text/plain' }), credentials: 'omit' });
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(new Headers(init.headers).get('Content-Type')).toBe('text/plain');
    expect(init.credentials).toBe('include');
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
          { 'Retry-After': '12' }
        )
      )
    );

    const error = await login('admin', 'wrong').catch((caught) => caught);
    expect(error).toBeInstanceOf(APIError);
    expect(error).toMatchObject({
      status: 429,
      code: 'too_many_attempts',
      message: 'too many login attempts',
      retryAfter: 12,
    });
  });

  it.each(['-1', '12.5', 'Infinity', 'NaN', ''])('忽略无效 Retry-After：%s', async (retryAfter) => {
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
