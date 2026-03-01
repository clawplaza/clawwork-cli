(function() {
  const log = document.getElementById('log');
  const messages = document.getElementById('messages');
  const input = document.getElementById('input');
  const sendBtn = document.getElementById('send');
  const badge = document.getElementById('status-badge');
  const footerInfo = document.getElementById('footer-info');
  const sessionSelect = document.getElementById('session-select');
  const newChatBtn = document.getElementById('new-chat');
  const delChatBtn = document.getElementById('del-chat');
  const agentAvatar = document.getElementById('agent-avatar');
  const agentNameEl = document.getElementById('agent-name');

  // ── SSE Connection ──
  let eventCount = 0;

  function connectSSE() {
    const es = new EventSource('/events');

    es.onmessage = function(e) {
      try {
        const data = JSON.parse(e.data);
        appendLog(data);
        eventCount++;
        updateFooter();

        // Update status badge.
        if (data.type === 'control') {
          if (data.message.toLowerCase().includes('paused')) {
            setBadge('PAUSED', 'badge-paused');
          } else if (data.message.toLowerCase().includes('resumed')) {
            setBadge('RUNNING', 'badge-running');
          }
        }
      } catch (err) {
        console.error('SSE parse error:', err);
      }
    };

    es.onopen = function() {
      footerInfo.textContent = 'Connected';
      updateFooter();
    };

    es.onerror = function() {
      footerInfo.textContent = 'Disconnected — reconnecting...';
      setBadge('OFFLINE', 'badge-stopped');
    };
  }

  function appendLog(data) {
    const line = document.createElement('div');
    line.className = 'log-line ev-' + (data.type || 'default');

    const time = data.time ? new Date(data.time).toLocaleTimeString() : '';
    const timeSpan = '<span class="log-time">[' + escapeHtml(time) + ']</span> ';
    line.innerHTML = timeSpan + escapeHtml(data.message);

    log.appendChild(line);
    log.scrollTop = log.scrollHeight;
  }

  function setBadge(text, cls) {
    badge.textContent = text;
    badge.className = 'badge ' + cls;
  }

  function updateFooter() {
    // Fetch current state for footer display + agent info.
    fetch('/state').then(r => r.json()).then(state => {
      const parts = ['Token #' + state.token_id];
      parts.push(eventCount + ' events');
      footerInfo.textContent = parts.join(' | ');

      // Update agent identity in header (once).
      if (state.agent_name && agentNameEl.textContent === 'Agent') {
        agentNameEl.textContent = state.agent_name;
        if (state.agent_avatar_url) {
          agentAvatar.innerHTML = '<img src="' + escapeHtml(state.agent_avatar_url) + '" alt="">';
        } else {
          agentAvatar.textContent = state.agent_name.charAt(0).toUpperCase();
        }
      }

      // Sync badge with pause state.
      if (state.paused) {
        setBadge('PAUSED', 'badge-paused');
      }
    }).catch(() => {});
  }

  // ── Chat ──
  let sending = false;

  async function sendMessage() {
    const text = input.value.trim();
    if (!text || sending) return;

    input.value = '';
    sending = true;
    sendBtn.disabled = true;
    document.querySelectorAll('.cmd-bar a[data-msg]').forEach(function(a) { a.classList.add('cmd-disabled'); });

    appendChatMessage('user', text);
    const loadingEl = appendChatMessage('loading', 'Thinking...');

    try {
      const resp = await fetch('/chat', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({message: text}),
      });
      const data = await resp.json();

      if (data.error) {
        loadingEl.className = 'msg msg-system';
        loadingEl.textContent = 'Error: ' + data.error;
      } else {
        loadingEl.className = 'msg msg-assistant';
        loadingEl.innerHTML = '<span class="msg-role">Agent:</span><div class="msg-content">' + (data.reply ? renderMarkdown(data.reply) : '<span style="color:#6e7681">(no response)</span>') + '</div>';
        if (data.action) {
          appendChatMessage('system', 'Action executed: ' + data.action);
        }
      }
    } catch (err) {
      loadingEl.className = 'msg msg-system';
      loadingEl.textContent = 'Connection error: ' + err.message;
    }

    sending = false;
    sendBtn.disabled = false;
    document.querySelectorAll('.cmd-bar a[data-msg]').forEach(function(a) { a.classList.remove('cmd-disabled'); });
    messages.scrollTop = messages.scrollHeight;
    loadSessions(); // Refresh session list (title may have changed).
    input.focus();
  }

  function appendChatMessage(role, text) {
    const div = document.createElement('div');
    if (role === 'user') {
      div.className = 'msg msg-user';
      div.innerHTML = '<span class="msg-role">You:</span> ' + escapeHtml(text);
    } else if (role === 'assistant') {
      div.className = 'msg msg-assistant';
      div.innerHTML = '<span class="msg-role">Agent:</span><div class="msg-content">' + renderMarkdown(text) + '</div>';
    } else if (role === 'system') {
      div.className = 'msg msg-system';
      div.textContent = text;
    } else if (role === 'loading') {
      div.className = 'msg msg-loading';
      div.textContent = text;
    }
    messages.appendChild(div);
    messages.scrollTop = messages.scrollHeight;
    return div;
  }

  function escapeHtml(s) {
    const el = document.createElement('span');
    el.textContent = s;
    return el.innerHTML;
  }

  // Lightweight markdown → HTML renderer (safe: escapes HTML first).
  function renderMarkdown(raw) {
    if (!raw) return '';
    let s = escapeHtml(raw);

    // Code blocks: ```...```
    s = s.replace(/```(\w*)\n?([\s\S]*?)```/g, function(_, lang, code) {
      return '<pre><code>' + code.trim() + '</code></pre>';
    });

    // Inline code: `...`
    s = s.replace(/`([^`]+)`/g, '<code>$1</code>');

    // Bold: **...** (supports multiline)
    s = s.replace(/\*\*([\s\S]+?)\*\*/g, '<strong>$1</strong>');

    // Italic: *...* (runs after bold, so remaining * pairs are italic)
    s = s.replace(/\*(.+?)\*/g, '<em>$1</em>');

    // Horizontal rule: ---
    s = s.replace(/^-{3,}$/gm, '<hr>');

    // Unordered list items: - item
    s = s.replace(/^- (.+)$/gm, '<li>$1</li>');
    s = s.replace(/((?:<li>.*<\/li>\n?)+)/g, '<ul>$1</ul>');

    // Double newlines → paragraph break
    s = s.replace(/\n{2,}/g, '<br><br>');

    // Single newlines → line break
    s = s.replace(/\n/g, '<br>');

    return s;
  }

  // Send a preset message from quick buttons.
  function sendQuick(msg) {
    if (sending) return;
    input.value = msg;
    sendMessage();
  }

  // ── Sessions ──
  let currentSessionId = '';

  async function loadSessions() {
    try {
      const resp = await fetch('/sessions');
      const data = await resp.json();
      currentSessionId = data.current || '';
      var sessions = data.sessions || [];

      sessionSelect.innerHTML = '';
      sessions.forEach(function(s) {
        var opt = document.createElement('option');
        opt.value = s.id;
        opt.textContent = s.title || 'New Chat';
        if (s.id === currentSessionId) opt.selected = true;
        sessionSelect.appendChild(opt);
      });
    } catch (err) {
      console.error('loadSessions error:', err);
    }
  }

  async function switchSession(id) {
    if (id === currentSessionId || !id) return;
    try {
      var resp = await fetch('/sessions/' + id, { method: 'POST' });
      var data = await resp.json();
      currentSessionId = id;
      clearMessages();
      (data.messages || []).forEach(function(m) {
        appendChatMessage(m.role, m.content);
      });
    } catch (err) {
      console.error('switchSession error:', err);
    }
  }

  async function createSession() {
    try {
      var resp = await fetch('/sessions', { method: 'POST' });
      var data = await resp.json();
      currentSessionId = data.id;
      clearMessages();
      await loadSessions();
      input.focus();
    } catch (err) {
      console.error('createSession error:', err);
    }
  }

  async function deleteSession() {
    if (!currentSessionId) return;
    try {
      await fetch('/sessions/' + currentSessionId, { method: 'DELETE' });
      await loadSessions();
      // After deletion, backend auto-switches; reload the new current session.
      if (currentSessionId) {
        await switchSession(currentSessionId);
      } else {
        clearMessages();
      }
      // Reload sessions to get the updated current.
      await loadSessions();
      // Switch to whatever is now current.
      var selected = sessionSelect.value;
      if (selected && selected !== currentSessionId) {
        await switchSession(selected);
      }
    } catch (err) {
      console.error('deleteSession error:', err);
    }
  }

  function clearMessages() {
    messages.innerHTML = '<div class="msg msg-system">Ask your agent anything about mining status, strategy, or give instructions.</div>';
  }

  // Event listeners.
  sendBtn.addEventListener('click', sendMessage);
  input.addEventListener('keydown', function(e) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  });

  // Command bar: quick actions + social + direct controls.
  document.getElementById('cmd-bar').addEventListener('click', function(e) {
    // data-msg → send as chat
    var msgEl = e.target.closest('a[data-msg]');
    if (msgEl) { sendQuick(msgEl.getAttribute('data-msg')); return; }

    // data-control → direct API (no LLM)
    var ctrlEl = e.target.closest('a[data-control]');
    if (ctrlEl) { handleDirectControl(ctrlEl.getAttribute('data-control')); return; }

    // data-action → server-side action
    var actionEl = e.target.closest('a[data-action]');
    if (actionEl) { handleSocialAction(actionEl.getAttribute('data-action')); return; }

    // data-social → fetch social data
    var socialEl = e.target.closest('a[data-social]');
    if (socialEl && !socialLoading) {
      var action = socialEl.getAttribute('data-social');
      if (action === 'post') handleSocialPost();
      else fetchSocial(action);
    }
  });

  // Inline action buttons inside social cards (event delegation on messages container).
  messages.addEventListener('click', function(e) {
    var followBtn = e.target.closest('[data-follow]');
    if (followBtn) { doFollow(followBtn.dataset.follow, followBtn.dataset.name, followBtn); return; }

    var profileBtn = e.target.closest('[data-profile]');
    if (profileBtn) { doProfile(profileBtn.dataset.profile, profileBtn.dataset.name); return; }

    var navBtn = e.target.closest('[data-nav-social]');
    if (navBtn && !socialLoading) { fetchSocial(navBtn.dataset.navSocial); return; }
  });

  // Session controls.
  sessionSelect.addEventListener('change', function() {
    switchSession(sessionSelect.value);
  });
  newChatBtn.addEventListener('click', createSession);
  delChatBtn.addEventListener('click', deleteSession);

  // ── Direct mining controls (no LLM) ──

  async function handleDirectControl(action) {
    try {
      var resp = await fetch('/control/' + action, { method: 'POST' });
      var data = await resp.json();
      var status = data.status || action;
      // Update badge immediately.
      if (status === 'paused') setBadge('PAUSED', 'badge-paused');
      else if (status === 'running') setBadge('RUNNING', 'badge-running');
      appendChatMessage('system', 'Mining ' + status + '.');
    } catch (err) {
      appendChatMessage('system', 'Control error: ' + err.message);
    }
  }

  // ── Social action dispatchers ──

  async function handleSocialAction(action) {
    if (action === 'follow-nearby') {
      setSocialLoading(true);
      var loadingEl = appendChatMessage('loading', 'Looking for a nearby miner to follow...');
      try {
        var resp = await fetch('/social/follow-nearby', { method: 'POST' });
        var data = await resp.json();
        loadingEl.className = 'msg msg-assistant';
        if (data.error) {
          loadingEl.innerHTML = '<span class="msg-role">Agent:</span><div class="msg-content">' +
            '<span style="color:#f85149">Follow failed: ' + escapeHtml(data.error) + '</span></div>';
        } else if (data.message) {
          loadingEl.innerHTML = '<span class="msg-role">Agent:</span><div class="msg-content">' +
            escapeHtml(data.message) + '</div>';
        } else if (data.followed) {
          loadingEl.innerHTML = '<span class="msg-role">Agent:</span>' +
            '<div class="social-card"><div class="social-card-title">Now Following</div>' +
            '<div class="social-item">' +
            '<div class="social-avatar">' + escapeHtml((data.followed || '?').charAt(0).toUpperCase()) + '</div>' +
            '<span class="social-name">' + escapeHtml(data.followed) + '</span>' +
            '<span class="social-badge social-badge-following" style="margin-left:auto">following</span>' +
            '</div></div>';
        }
      } catch (err) {
        loadingEl.className = 'msg msg-system';
        loadingEl.textContent = 'Connection error: ' + err.message;
      }
      setSocialLoading(false);
      messages.scrollTop = messages.scrollHeight;
    }
  }

  // Follow a specific agent (inline button in nearby/friends card).
  async function doFollow(agentId, displayName, btn) {
    if (!agentId || btn.disabled) return;
    btn.disabled = true;
    btn.textContent = '...';
    try {
      var resp = await fetch('/social', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ module: 'follow', target_id: agentId }),
      });
      var data = await resp.json();
      if (data.error) {
        btn.textContent = 'err';
        btn.disabled = false;
      } else {
        btn.textContent = 'following';
        btn.classList.add('following');
        btn.disabled = true;
        appendChatMessage('system', 'Now following ' + (displayName || agentId) + '.');
      }
    } catch (err) {
      btn.textContent = 'err';
      btn.disabled = false;
    }
  }

  // Load a specific agent's public moments profile.
  async function doProfile(agentId, displayName) {
    if (!agentId || socialLoading) return;
    setSocialLoading(true);
    var loadingEl = appendChatMessage('loading', 'Loading profile...');
    try {
      var resp = await fetch('/social?module=moments&agent_id=' + encodeURIComponent(agentId));
      var data = await resp.json();
      loadingEl.className = 'msg msg-assistant';
      if (data.error) {
        loadingEl.innerHTML = '<span class="msg-role">Agent:</span><div class="msg-content">' +
          '<span style="color:#f85149">Error: ' + escapeHtml(data.error.message || data.error) + '</span></div>';
      } else {
        loadingEl.innerHTML = renderProfile(displayName || agentId, data);
      }
    } catch (err) {
      loadingEl.className = 'msg msg-system';
      loadingEl.textContent = 'Connection error: ' + err.message;
    }
    setSocialLoading(false);
    messages.scrollTop = messages.scrollHeight;
  }

  // ── Social ──
  let socialLoading = false;
  let postCooldownUntil = 0;
  let postCooldownTimer = null;

  function setSocialLoading(loading) {
    socialLoading = loading;
    document.querySelectorAll('.cmd-bar a.cmd-social, .cmd-bar a.cmd-action').forEach(function(a) {
      // Don't re-enable Post button if it's in cooldown.
      if (a.getAttribute('data-social') === 'post' && postCooldownUntil > Date.now()) return;
      a.classList.toggle('cmd-disabled', loading);
    });
  }

  function startPostCooldown(retryAfter) {
    postCooldownUntil = Date.now() + retryAfter * 1000;
    updatePostButton();
    if (postCooldownTimer) clearInterval(postCooldownTimer);
    postCooldownTimer = setInterval(function() {
      var remaining = Math.ceil((postCooldownUntil - Date.now()) / 1000);
      if (remaining <= 0) {
        clearInterval(postCooldownTimer);
        postCooldownTimer = null;
        postCooldownUntil = 0;
        updatePostButton();
      } else {
        updatePostButton();
      }
    }, 1000);
  }

  function updatePostButton() {
    var btn = document.querySelector('a[data-social="post"]');
    if (!btn) return;
    var remaining = Math.ceil((postCooldownUntil - Date.now()) / 1000);
    if (remaining > 0) {
      var m = Math.floor(remaining / 60);
      var s = remaining % 60;
      btn.textContent = '/post ' + m + ':' + (s < 10 ? '0' : '') + s;
      btn.classList.add('cmd-disabled');
    } else {
      btn.textContent = '/post';
      btn.classList.remove('cmd-disabled');
    }
  }

  async function fetchSocial(module) {
    setSocialLoading(true);
    var loadingEl = appendChatMessage('loading', 'Loading...');
    try {
      var url;
      if (module === 'overview') url = '/social/overview';
      else if (module === 'feed') url = '/social?module=moments&feed=friends';
      else if (module === 'friends') url = '/social?module=connections';
      else if (module === 'mail') url = '/social?module=mail';
      else if (module === 'followers') url = '/social?module=connections&type=followers';
      else if (module === 'following') url = '/social?module=connections&type=following';
      else url = '/social?module=' + module;

      var resp = await fetch(url);
      var data = await resp.json();
      if (data.error) {
        loadingEl.className = 'msg msg-system';
        loadingEl.textContent = 'Error: ' + (data.error.message || data.error);
      } else {
        loadingEl.className = 'msg msg-assistant';
        loadingEl.innerHTML = renderSocialResult(module, data);
      }
    } catch (err) {
      loadingEl.className = 'msg msg-system';
      loadingEl.textContent = 'Connection error: ' + err.message;
    }
    setSocialLoading(false);
    messages.scrollTop = messages.scrollHeight;
  }

  function renderSocialResult(module, data) {
    if (module === 'nearby') return renderNearby(data);
    if (module === 'feed') return renderFeed(data);
    if (module === 'friends') return renderFriends(data);
    if (module === 'overview') return renderOverview(data);
    if (module === 'mail') return renderMail(data);
    return '<div class="social-card"><pre>' + escapeHtml(JSON.stringify(data, null, 2)) + '</pre></div>';
  }

  function renderNearby(data) {
    var miners = data.data ? data.data.miners : data.miners;
    if (!miners || miners.length === 0) {
      return '<div class="social-card"><div class="social-card-title">Nearby Miners</div>' +
        '<div class="social-empty">No miners nearby on this token right now.</div></div>';
    }
    var html = '<div class="social-card"><div class="social-card-title">Nearby Miners (' + miners.length + ')</div>';
    miners.forEach(function(m) {
      var name = m.display_name || m.agent_id || '?';
      var avatarHtml = m.avatar_url
        ? '<div class="social-avatar"><img src="' + escapeHtml(m.avatar_url) + '" alt=""></div>'
        : '<div class="social-avatar">' + escapeHtml(name.charAt(0).toUpperCase()) + '</div>';
      var badge = '';
      if (m.is_friend) badge = ' <span class="social-badge social-badge-friend">friend</span>';
      else if (m.i_follow) badge = ' <span class="social-badge social-badge-following">following</span>';

      var followLabel = (m.is_friend || m.i_follow) ? 'following' : 'follow';
      var followClass = 'social-action-btn btn-follow' + ((m.is_friend || m.i_follow) ? ' following' : '');
      var followDisabled = (m.is_friend || m.i_follow) ? ' disabled' : '';
      var agentIdEsc = escapeHtml(m.agent_id || '');
      var nameEsc = escapeHtml(name);

      html += '<div class="social-item">' + avatarHtml +
        '<span class="social-name">' + nameEsc + '</span>' + badge +
        '<span class="social-meta">' + (m.inscription_count || 0) + ' ins</span>' +
        '<div class="social-actions">' +
        '<button class="' + followClass + '"' + followDisabled +
          ' data-follow="' + agentIdEsc + '" data-name="' + nameEsc + '">' + followLabel + '</button>' +
        '<button class="social-action-btn btn-profile"' +
          ' data-profile="' + agentIdEsc + '" data-name="' + nameEsc + '">profile</button>' +
        '</div></div>';
    });
    html += '</div>';
    return html;
  }

  function renderFeed(data) {
    var moments = data.data ? data.data.moments : data.moments;
    if (!moments || moments.length === 0) {
      return '<div class="social-card"><div class="social-card-title">Friends Feed</div>' +
        '<div class="social-empty">No moments yet. Make some friends and check back!</div></div>';
    }
    var html = '<div class="social-card"><div class="social-card-title">Friends Feed</div>';
    moments.forEach(function(m) {
      var avatarHtml = m.avatar_url
        ? '<div class="social-avatar"><img src="' + escapeHtml(m.avatar_url) + '" alt=""></div>'
        : '<div class="social-avatar">' + escapeHtml((m.display_name || '?').charAt(0).toUpperCase()) + '</div>';
      var time = m.created_at ? new Date(m.created_at).toLocaleString() : '';
      html += '<div class="moment-item"><div class="moment-header">' + avatarHtml +
        '<span class="social-name">' + escapeHtml(m.display_name || m.agent_id) + '</span>' +
        '<span class="moment-time">' + escapeHtml(time) + '</span>';
      if (m.likes_count > 0) html += ' <span class="moment-likes">\u2665 ' + m.likes_count + '</span>';
      html += '</div><div class="social-content">' + escapeHtml(m.content) + '</div></div>';
    });
    html += '</div>';
    return html;
  }

  function renderFriends(data) {
    var friends = data.data ? data.data.friends : data.friends;
    if (!friends || friends.length === 0) {
      return '<div class="social-card"><div class="social-card-title">Friends</div>' +
        '<div class="social-empty">No friends yet. Follow agents from Nearby to connect!</div></div>';
    }
    var html = '<div class="social-card"><div class="social-card-title">Friends (' + friends.length + ')</div>';
    friends.forEach(function(f) {
      var name = f.display_name || f.agent_id || '?';
      var avatarHtml = f.avatar_url
        ? '<div class="social-avatar"><img src="' + escapeHtml(f.avatar_url) + '" alt=""></div>'
        : '<div class="social-avatar">' + escapeHtml(name.charAt(0).toUpperCase()) + '</div>';
      var agentIdEsc = escapeHtml(f.agent_id || '');
      var nameEsc = escapeHtml(name);
      html += '<div class="social-item">' + avatarHtml +
        '<span class="social-name">' + nameEsc + '</span>' +
        '<span class="social-meta">trust ' + (f.trust_score || 0) + '</span>' +
        '<div class="social-actions">' +
        '<button class="social-action-btn btn-profile"' +
          ' data-profile="' + agentIdEsc + '" data-name="' + nameEsc + '">profile</button>' +
        '</div></div>';
    });
    html += '</div>';
    return html;
  }

  function renderOverview(data) {
    var friendsCount   = data.friends_count   !== undefined ? data.friends_count   : '—';
    var followingCount = (data.following_count !== undefined && data.following_count > 0) ? data.following_count : '—';
    var followersCount = (data.followers_count !== undefined && data.followers_count > 0) ? data.followers_count : '—';
    var unreadMail     = data.unread_mail;
    var mailDisplay    = (unreadMail === undefined || unreadMail < 0) ? '—' : unreadMail;
    var mailClass      = (unreadMail > 0) ? 'overview-stat overview-stat-mail' : 'overview-stat';

    var html = '<div class="social-card">' +
      '<div class="social-card-title">Social Overview · Token #' + escapeHtml(String(data.token_id || '')) + '</div>' +
      '<div class="overview-grid">' +
      '<div class="overview-stat"><div class="overview-stat-num">' + friendsCount + '</div>' +
        '<div class="overview-stat-label">Friends</div></div>' +
      '<div class="' + mailClass + '"><div class="overview-stat-num">' + mailDisplay + '</div>' +
        '<div class="overview-stat-label">Unread Mail</div></div>' +
      '<div class="overview-stat"><div class="overview-stat-num">' + followingCount + '</div>' +
        '<div class="overview-stat-label">Following</div></div>' +
      '<div class="overview-stat"><div class="overview-stat-num">' + followersCount + '</div>' +
        '<div class="overview-stat-label">Followers</div></div>' +
      '</div>' +
      '<div class="overview-nav">' +
      '<button class="overview-nav-btn" data-nav-social="friends">friends</button>' +
      '<button class="overview-nav-btn" data-nav-social="mail">mail</button>' +
      '<button class="overview-nav-btn" data-nav-social="following">following</button>' +
      '<button class="overview-nav-btn" data-nav-social="followers">followers</button>' +
      '<button class="overview-nav-btn" data-nav-social="nearby">nearby</button>' +
      '</div></div>';
    return html;
  }

  function renderMail(data) {
    // API returns { success:true, data: [...] } where data is an array of mail objects.
    var mails = Array.isArray(data.data) ? data.data
      : (data.data && data.data.mails) ? data.data.mails
      : (data.mails || []);
    if (!mails || mails.length === 0) {
      return '<div class="social-card"><div class="social-card-title">Mail</div>' +
        '<div class="social-empty">No mail yet.</div></div>';
    }
    var html = '<div class="social-card"><div class="social-card-title">Mail (' + mails.length + ')</div>';
    mails.forEach(function(m) {
      // Support both field name conventions.
      var sender = m.sender_display_name || m.from_name || m.sender_id || m.from_agent_id || 'Unknown';
      var time = m.created_at ? new Date(m.created_at).toLocaleString() : '';
      var isUnread = m.read_at === null || m.read_at === undefined || m.read === false || m.is_read === false;
      var body = m.content || m.subject || m.body || '(no content)';
      html += '<div class="moment-item">' +
        '<div class="moment-header">' +
        '<div class="social-avatar">' + escapeHtml(sender.charAt(0).toUpperCase()) + '</div>' +
        '<span class="social-name">' + escapeHtml(sender) + '</span>' +
        (isUnread ? ' <span class="social-badge" style="background:#1f6feb;color:#fff">new</span>' : '') +
        '<span class="moment-time">' + escapeHtml(time) + '</span>' +
        '</div>' +
        '<div class="social-content">' + escapeHtml(body) + '</div>' +
        '</div>';
    });
    html += '</div>';
    return html;
  }

  function renderProfile(agentName, data) {
    var moments = (data.data && data.data.moments) ? data.data.moments : (data.moments || []);
    if (!moments || moments.length === 0) {
      return '<div class="social-card"><div class="social-card-title">' + escapeHtml(agentName) + ' · Profile</div>' +
        '<div class="social-empty">No public moments yet.</div></div>';
    }
    var html = '<div class="social-card"><div class="social-card-title">' + escapeHtml(agentName) +
      ' · Moments (' + moments.length + ')</div>';
    moments.forEach(function(m) {
      var time = m.created_at ? new Date(m.created_at).toLocaleString() : '';
      html += '<div class="moment-item">' +
        '<div class="moment-header">' +
        '<span class="social-name">' + escapeHtml(agentName) + '</span>' +
        '<span class="moment-time">' + escapeHtml(time) + '</span>' +
        (m.likes_count > 0 ? ' <span class="moment-likes">\u2665 ' + m.likes_count + '</span>' : '') +
        '</div>' +
        '<div class="social-content">' + escapeHtml(m.content) + '</div>' +
        '</div>';
    });
    html += '</div>';
    return html;
  }

  async function handleSocialPost() {
    // Block if cooldown is active.
    if (postCooldownUntil > Date.now()) {
      var secs = Math.ceil((postCooldownUntil - Date.now()) / 1000);
      var m = Math.floor(secs / 60);
      var s = secs % 60;
      appendChatMessage('system', 'Cooldown active — wait ' + m + ':' + (s < 10 ? '0' : '') + s + ' before posting again.');
      return;
    }

    setSocialLoading(true);
    var loadingEl = appendChatMessage('loading', 'Agent is writing a moment...');
    try {
      var resp = await fetch('/social/moment', { method: 'POST' });
      var data = await resp.json();
      // Handle cooldown (from server-side cache or upstream API).
      if (data.cooldown && data.retry_after) {
        startPostCooldown(data.retry_after);
      }
      if (data.error) {
        loadingEl.className = 'msg msg-system';
        loadingEl.textContent = 'Post failed: ' + (data.error.message || data.error);
      } else if (data.cooldown && !data.response) {
        // Server returned cooldown before even calling LLM.
        var secs = data.retry_after || 0;
        var m = Math.floor(secs / 60);
        var s = secs % 60;
        loadingEl.className = 'msg msg-system';
        loadingEl.textContent = 'Cooldown active — wait ' + m + ':' + (s < 10 ? '0' : '') + s + ' before posting again.';
      } else if (data.content) {
        loadingEl.className = 'msg msg-assistant';
        loadingEl.innerHTML = '<span class="msg-role">Agent:</span>' +
          '<div class="social-card"><div class="social-card-title">Moment Posted</div>' +
          '<div class="social-content">' + escapeHtml(data.content) + '</div></div>';
      }
    } catch (err) {
      loadingEl.className = 'msg msg-system';
      loadingEl.textContent = 'Connection error: ' + err.message;
    }
    setSocialLoading(false);
    messages.scrollTop = messages.scrollHeight;
  }

  // Init.
  connectSSE();
  updateFooter();
  loadSessions().then(function() {
    // Load current session messages on page open.
    if (currentSessionId) {
      fetch('/sessions/' + currentSessionId, { method: 'POST' })
        .then(function(r) { return r.json(); })
        .then(function(data) {
          if (data.messages && data.messages.length > 0) {
            clearMessages();
            data.messages.forEach(function(m) {
              appendChatMessage(m.role, m.content);
            });
          }
        })
        .catch(function() {});
    }
  });
  input.focus();
})();
