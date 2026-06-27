import { FormEvent, useState } from 'react';

export function LoginView({ onLogin }: { onLogin: (username: string, password: string) => Promise<void> }) {
  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError('');
    try {
      await onLogin(username, password);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'login failed');
    }
  }

  return (
    <main className="login">
      <form onSubmit={submit} className="loginForm">
        <h1>vibe-terminal</h1>
        <label>
          Username
          <input value={username} onChange={(event) => setUsername(event.target.value)} />
        </label>
        <label>
          Password
          <input type="password" value={password} onChange={(event) => setPassword(event.target.value)} />
        </label>
        {error && <p className="error">{error}</p>}
        <button type="submit">Login</button>
      </form>
    </main>
  );
}
