// Shared by every page. Resolves to the signed-in user, or null.
const currentUser = fetch('/me').then(res => res.ok ? res.json() : null).catch(() => null);

function signInURL() {
  return `/auth/google?${new URLSearchParams({ next: location.pathname + location.search })}`;
}

async function signOut() {
  await fetch('/auth/logout', { method: 'POST' });
  location.reload();
}

async function renderAccount() {
  const account = document.getElementById('account');
  const user = await currentUser;
  if (!user) {
    const link = document.createElement('a');
    link.className = 'button button-quiet';
    link.href = signInURL();
    link.textContent = 'Sign in';
    account.append(link);
    return;
  }
  if (user.picture) {
    const photo = document.createElement('img');
    photo.className = 'avatar';
    photo.src = user.picture;
    photo.alt = '';
    // Google refuses profile photo requests that carry a referrer.
    photo.referrerPolicy = 'no-referrer';
    account.append(photo);
  }
  const name = document.createElement('span');
  name.className = 'account-name';
  name.textContent = user.name;
  const out = document.createElement('button');
  out.className = 'button button-quiet';
  out.textContent = 'Sign out';
  out.addEventListener('click', signOut);
  account.append(name, out);
}

renderAccount();
