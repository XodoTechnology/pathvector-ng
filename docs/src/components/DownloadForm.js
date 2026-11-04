import Admonition from '@theme/Admonition';
import React from "react";

export const DownloadForm = () => (
    <Admonition type="note" title="Download">
        <p>All Pathvector NG builds — binaries, <code>.deb</code>/<code>.rpm</code> packages, and vendor platform
            images (Arista, Juniper, MikroTik, Cisco IOx) — are published on GitHub.</p>
        <div className="download-form">
            <div className="download-col">
                <button onClick={() => {
                    window.open("https://github.com/XodoTechnology/pathvector-ng/releases/latest", "_blank");
                }}>Latest release
                </button>
            </div>
            <div className="download-col">
                <button onClick={() => {
                    window.open("https://github.com/XodoTechnology/pathvector-ng/releases", "_blank");
                }}>All releases
                </button>
            </div>
        </div>
    </Admonition>
)
