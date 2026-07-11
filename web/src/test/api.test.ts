import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  APIError,
  disableTwoFactor,
  enableTwoFactor,
  getTwoFactorStatus,
  login,
  me,
  regenerateRecoveryCodes,
  startTwoFactorSetup,
  verifyTwoFactor,
} from '../api';
import { jsonRequestHeaders } from '../api-internals';

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

  it.each([1.5, Number.MAX_SAFE_INTEGER + 1])('拒绝不安全的 expires_in：%s', async (expiresIn) => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(202, {
          two_factor_required: true,
          challenge_token: 'challenge-1',
          expires_in: expiresIn,
        })
      )
    );

    await expect(login('admin', 'secret')).rejects.toMatchObject({ code: 'invalid_response' });
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
    const sent = jsonRequestHeaders(headers);
    expect(sent.get('Accept')).toBe(expected);
    expect(sent.get('Content-Type')).toBe('application/json');
  });

  it('保留调用方显式 Content-Type', async () => {
    expect(jsonRequestHeaders(new Headers({ 'Content-Type': 'text/plain' })).get('Content-Type')).toBe('text/plain');
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

describe('二因素管理 API', () => {
  it('按后端契约发送五个请求并解析有效响应', async () => {
    const recoveryCodes = Array.from({ length: 10 }, (_, index) => `CODE-${index + 1}`);
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(200, { enabled: true, recovery_codes_remaining: 7 }))
      .mockResolvedValueOnce(
        jsonResponse(200, {
          manual_key: 'MANUALKEY',
          otpauth_uri: 'otpauth://totp/Vibe%20Terminal:admin?secret=MANUALKEY',
          expires_at: '2026-07-11T15:00:00Z',
        })
      )
      .mockResolvedValueOnce(jsonResponse(200, { recovery_codes: recoveryCodes }))
      .mockResolvedValueOnce(jsonResponse(200, { recovery_codes: recoveryCodes }))
      .mockResolvedValueOnce(jsonResponse(200, { ok: true }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(getTwoFactorStatus()).resolves.toEqual({ enabled: true, recoveryCodesRemaining: 7 });
    await expect(startTwoFactorSetup('secret')).resolves.toEqual({
      manualKey: 'MANUALKEY',
      otpauthURI: 'otpauth://totp/Vibe%20Terminal:admin?secret=MANUALKEY',
      expiresAt: '2026-07-11T15:00:00Z',
    });
    await expect(enableTwoFactor('123456')).resolves.toEqual(recoveryCodes);
    await expect(regenerateRecoveryCodes('secret', '654321')).resolves.toEqual(recoveryCodes);
    await expect(disableTwoFactor('secret')).resolves.toBeUndefined();

    const expectedRequests = [
      ['/api/security/2fa', { method: 'GET' }],
      ['/api/security/2fa/setup', { method: 'POST', body: JSON.stringify({ password: 'secret' }) }],
      ['/api/security/2fa/enable', { method: 'POST', body: JSON.stringify({ code: '123456' }) }],
      [
        '/api/security/2fa/recovery-codes',
        { method: 'POST', body: JSON.stringify({ password: 'secret', code: '654321' }) },
      ],
      ['/api/security/2fa/disable', { method: 'POST', body: JSON.stringify({ password: 'secret' }) }],
    ] as const;
    expect(fetchMock).toHaveBeenCalledTimes(expectedRequests.length);
    expectedRequests.forEach(([path, expected], index) => {
      const [actualPath, init] = fetchMock.mock.calls[index] as [string, RequestInit];
      expect(actualPath).toBe(path);
      expect(init).toMatchObject({ ...expected, credentials: 'include' });
      expect(new Headers(init.headers).get('Content-Type')).toBe('application/json');
    });
  });

  it.each([
    ['status 字段错误', getTwoFactorStatus, { enabled: 'yes', recovery_codes_remaining: 0 }],
    ['status 数量越界', getTwoFactorStatus, { enabled: true, recovery_codes_remaining: 11 }],
    [
      'setup 字段错误',
      () => startTwoFactorSetup('secret'),
      { manual_key: '', otpauth_uri: 'otpauth://totp/example', expires_at: 'not-a-date' },
    ],
    ['enable 不足十码', () => enableTwoFactor('123456'), { recovery_codes: Array(9).fill('CODE') }],
    [
      '轮换包含空码',
      () => regenerateRecoveryCodes('secret', '123456'),
      { recovery_codes: [...Array(9).fill('CODE'), ''] },
    ],
    ['disable 未确认', () => disableTwoFactor('secret'), { ok: false }],
  ])('拒绝异常成功响应：%s', async (_name, call, body) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, body)));
    await expect(call()).rejects.toMatchObject({ name: 'APIError', code: 'invalid_response' });
  });

  it('拒绝管理接口的意外成功状态，包括 disable 的 204', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })));
    await expect(disableTwoFactor('secret')).rejects.toMatchObject({ status: 204, code: 'invalid_response' });

    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse(201, { enabled: false, recovery_codes_remaining: 0 }))
    );
    await expect(getTwoFactorStatus()).rejects.toMatchObject({ status: 201, code: 'invalid_response' });
  });
});
