// Sol Frontend - Main Application Logic

class SolApp {
    constructor() {
        this.currentSection = 'browser';
        this.apps = [];
        this.games = [];
        this.wispClient = null;
        this.init();
    }

    async init() {
        await this.registerServiceWorker();
        await this.loadContent();
        this.setupEventListeners();
        this.renderGrid('apps', this.apps);
        this.renderGrid('games', this.games);
    }

    async registerServiceWorker() {
        if ('serviceWorker' in navigator) {
            try {
                const registration = await navigator.serviceWorker.register('/sw.js');
                console.log('Service Worker registered:', registration.scope);
            } catch (error) {
                console.error('Service Worker registration failed:', error);
            }
        }
    }

    async loadContent() {
        try {
            const [appsResponse, gamesResponse] = await Promise.all([
                fetch('apps.json'),
                fetch('games.json')
            ]);

            if (appsResponse.ok) {
                this.apps = await appsResponse.json();
            }

            if (gamesResponse.ok) {
                this.games = await gamesResponse.json();
            }
        } catch (error) {
            console.error('Failed to load content:', error);
        }
    }

    setupEventListeners() {
        document.querySelectorAll('.nav-links a').forEach(link => {
            link.addEventListener('click', (e) => {
                e.preventDefault();
                const section = link.dataset.section;
                this.switchSection(section);
            });
        });

        const urlForm = document.getElementById('url-form');
        const urlInput = document.getElementById('url-input');

        urlForm.addEventListener('submit', async (e) => {
            e.preventDefault();
            const url = urlInput.value.trim();
            if (url) {
                await this.navigateToUrl(url);
            }
        });

        urlInput.addEventListener('keypress', (e) => {
            if (e.key === 'Enter') {
                urlForm.dispatchEvent(new Event('submit'));
            }
        });
    }

    switchSection(sectionId) {
        document.querySelectorAll('.section').forEach(section => {
            section.classList.remove('active');
        });

        document.querySelectorAll('.nav-links a').forEach(link => {
            link.classList.remove('active');
        });

        document.getElementById(sectionId).classList.add('active');
        document.querySelector(`[data-section="${sectionId}"]`).classList.add('active');
        this.currentSection = sectionId;
    }

    async navigateToUrl(url) {
        let targetUrl = url;
        
        if (!url.startsWith('http://') && !url.startsWith('https://')) {
            targetUrl = 'https://' + url;
        }

        const urlObj = new URL(targetUrl);
        const proxyPath = `/proxy/${urlObj.hostname}${urlObj.pathname}${urlObj.search}`;
        
        try {
            const response = await fetch(proxyPath, {
                method: 'GET',
                headers: {
                    'X-Target-URL': targetUrl
                }
            });

            if (response.ok) {
                const html = await response.text();
                this.displayProxiedContent(html, targetUrl);
            } else {
                alert('Failed to load the page. Please try again.');
            }
        } catch (error) {
            console.error('Navigation error:', error);
            alert('Navigation failed. Ensure the Service Worker is active.');
        }
    }

    displayProxiedContent(html, url) {
        const browserSection = document.getElementById('browser');
        let iframe = browserSection.querySelector('iframe');
        
        if (!iframe) {
            iframe = document.createElement('iframe');
            iframe.style.cssText = 'width: 100%; height: calc(100vh - 200px); border: none; border-radius: 8px; background: white;';
            browserSection.appendChild(iframe);
        }

        const blob = new Blob([html], { type: 'text/html' });
        iframe.src = URL.createObjectURL(blob);
        
        document.getElementById('url-input').value = url;
    }

    renderGrid(type, items) {
        const grid = document.getElementById(`${type}-grid`);
        if (!grid || items.length === 0) return;

        grid.innerHTML = '';

        items.forEach(item => {
            const card = this.createCard(item, type);
            grid.appendChild(card);
        });
    }

    createCard(item, type) {
        const template = document.getElementById('card-template');
        const clone = template.content.cloneNode(true);
        const card = clone.querySelector('.card');

        const imageDiv = card.querySelector('.card-image');
        imageDiv.textContent = item.icon || '🌐';

        card.querySelector('.card-title').textContent = item.name;
        card.querySelector('.card-description').textContent = item.description || '';

        card.addEventListener('click', () => {
            this.launchApp(item.url, type);
        });

        return card;
    }

    async launchApp(url, type) {
        const urlObj = new URL(url.startsWith('http') ? url : `https://${url}`);
        const proxyPath = `/proxy/${urlObj.hostname}${urlObj.pathname}${urlObj.search}`;

        try {
            const response = await fetch(proxyPath);
            
            if (response.ok) {
                const html = await response.text();
                this.openInNewWindow(html, url);
            } else {
                window.open(url, '_blank');
            }
        } catch (error) {
            console.error('Launch error:', error);
            window.open(url, '_blank');
        }
    }

    openInNewWindow(html, originalUrl) {
        const win = window.open('', '_blank');
        if (win) {
            win.document.write(html);
            win.document.close();
        } else {
            window.open(originalUrl, '_blank');
        }
    }
}

document.addEventListener('DOMContentLoaded', () => {
    window.solApp = new SolApp();
});
