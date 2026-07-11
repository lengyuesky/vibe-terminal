import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { LanguageProvider, detectInitialLang, translate, useLang, useT } from '../i18n';

// 探针组件:暴露当前语言、一段翻译文本和切换按钮
function LangProbe() {
  const { t } = useT();
  const { lang, setLang } = useLang();
  return (
    <div>
      <span data-testid="lang">{lang}</span>
      <span data-testid="text">{t('nav.settings')}</span>
      <button type="button" onClick={() => setLang('zh')}>to-zh</button>
    </div>
  );
}

describe('i18n', () => {
  afterEach(() => {
    window.localStorage.clear();
    vi.restoreAllMocks();
    document.documentElement.lang = 'en';
  });

  it('localStorage 中的语言优先于浏览器语言', () => {
    window.localStorage.setItem('vibe.lang', 'zh');
    vi.spyOn(window.navigator, 'language', 'get').mockReturnValue('en-US');
    expect(detectInitialLang()).toBe('zh');
  });

  it('无存储时浏览器语言 zh-CN 检测为中文', () => {
    vi.spyOn(window.navigator, 'language', 'get').mockReturnValue('zh-CN');
    expect(detectInitialLang()).toBe('zh');
  });

  it('无存储且非中文浏览器语言兜底为英文', () => {
    vi.spyOn(window.navigator, 'language', 'get').mockReturnValue('fr-FR');
    expect(detectInitialLang()).toBe('en');
  });

  it('localStorage 值非法时忽略并按浏览器语言检测', () => {
    window.localStorage.setItem('vibe.lang', 'jp');
    vi.spyOn(window.navigator, 'language', 'get').mockReturnValue('zh-CN');
    expect(detectInitialLang()).toBe('zh');
  });

  it('translate 支持 {name} 插值', () => {
    expect(translate('zh', 'security.recoveryRemaining', { count: 3 })).toBe('剩余 3 个恢复码。');
    expect(translate('en', 'security.recoveryRemaining', { count: 3 })).toBe('3 recovery codes remaining.');
  });

  it('Provider 内切换语言即时生效并持久化', async () => {
    render(
      <LanguageProvider>
        <LangProbe />
      </LanguageProvider>
    );
    expect(screen.getByTestId('lang')).toHaveTextContent('en');
    expect(screen.getByTestId('text')).toHaveTextContent('Settings');
    await userEvent.click(screen.getByRole('button', { name: 'to-zh' }));
    expect(screen.getByTestId('lang')).toHaveTextContent('zh');
    expect(screen.getByTestId('text')).toHaveTextContent('设置');
    expect(window.localStorage.getItem('vibe.lang')).toBe('zh');
    expect(document.documentElement.lang).toBe('zh-CN');
  });

  it('无 Provider 时降级为英文且 setLang 不抛错', async () => {
    render(<LangProbe />);
    expect(screen.getByTestId('lang')).toHaveTextContent('en');
    expect(screen.getByTestId('text')).toHaveTextContent('Settings');
    await userEvent.click(screen.getByRole('button', { name: 'to-zh' }));
    expect(screen.getByTestId('lang')).toHaveTextContent('en');
  });

  it('localStorage 写入异常时切换仍生效(仅当次会话)', async () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('quota exceeded');
    });
    render(
      <LanguageProvider>
        <LangProbe />
      </LanguageProvider>
    );
    await userEvent.click(screen.getByRole('button', { name: 'to-zh' }));
    expect(screen.getByTestId('lang')).toHaveTextContent('zh');
  });
});
