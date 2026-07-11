import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { en } from './en';
import type { Translation, TranslationKey } from './en';
import { zh } from './zh';

export type Lang = 'en' | 'zh';
export type TFunction = (key: TranslationKey, params?: Record<string, string | number>) => string;
export type { Translation, TranslationKey };

const STORAGE_KEY = 'vibe.lang';
const dictionaries: Record<Lang, Translation> = { en, zh };

function readStoredLang(): Lang | null {
  try {
    const value = window.localStorage.getItem(STORAGE_KEY);
    return value === 'en' || value === 'zh' ? value : null;
  } catch {
    // localStorage 不可用(如隐私模式)时忽略
    return null;
  }
}

// 初始语言判定:localStorage 优先,其次浏览器语言,兜底英文
export function detectInitialLang(): Lang {
  const stored = readStoredLang();
  if (stored) return stored;
  const language = typeof navigator !== 'undefined' ? navigator.language : 'en';
  return language?.toLowerCase().startsWith('zh') ? 'zh' : 'en';
}

function htmlLang(lang: Lang): string {
  return lang === 'zh' ? 'zh-CN' : 'en';
}

// 翻译并做 {name} 占位符插值;未提供的占位符原样保留
export function translate(lang: Lang, key: TranslationKey, params?: Record<string, string | number>): string {
  const template = dictionaries[lang][key] ?? en[key] ?? key;
  if (!params) return template;
  return template.replace(/\{(\w+)\}/g, (match, name: string) =>
    name in params ? String(params[name]) : match
  );
}

type LanguageContextValue = {
  lang: Lang;
  setLang: (lang: Lang) => void;
};

const LanguageContext = createContext<LanguageContextValue | null>(null);

export function LanguageProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(detectInitialLang);

  // 同步 <html lang>,便于辅助技术与字体渲染
  useEffect(() => {
    document.documentElement.lang = htmlLang(lang);
  }, [lang]);

  const setLang = useCallback((next: Lang) => {
    setLangState(next);
    try {
      window.localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // 写入失败时仅当次会话生效
    }
  }, []);

  const value = useMemo(() => ({ lang, setLang }), [lang, setLang]);
  return <LanguageContext.Provider value={value}>{children}</LanguageContext.Provider>;
}

// 无 Provider 时降级为英文 + noop,让单测无需强制包裹
export function useLang(): LanguageContextValue {
  return useContext(LanguageContext) ?? { lang: 'en', setLang: () => undefined };
}

export function useT(): { t: TFunction; lang: Lang } {
  const { lang } = useLang();
  const t = useCallback<TFunction>((key, params) => translate(lang, key, params), [lang]);
  return { t, lang };
}
