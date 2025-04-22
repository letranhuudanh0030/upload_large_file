const CHUNK_SIZE = 5 * 1024 * 1024; // 5MB

async function startUpload() {
    const file = document.getElementById('fileInput').files[0];
    if (!file) return;

    // Encode tên file chuẩn URL-safe
    const fileId = btoa(encodeURIComponent(file.name))
        .replace(/\+/g, '-')
        .replace(/\//g, '_')
        .replace(/=+$/, '');

    try {
        const totalChunks = Math.ceil(file.size / CHUNK_SIZE);
        const uploadState = {
            uploadedChunks: [],
            totalChunks,
            fileName: file.name // Lưu tên file gốc
        };
        localStorage.setItem(fileId, JSON.stringify(uploadState));

        // Upload parallel với Promise.all
        const uploadPromises = [];
        for (let i = 0; i < totalChunks; i++) {
            if (uploadState.uploadedChunks.includes(i)) continue;

            const chunk = file.slice(i * CHUNK_SIZE, (i + 1) * CHUNK_SIZE);
            const fileHash = await calculateFileHash(file);
            const formData = new FormData();
            formData.append('fileId', fileId);
            formData.append('chunkIndex', i);
            formData.append('chunk', chunk);
            formData.append('file_hash', fileHash);

            uploadPromises.push(
                fetch('http://localhost:8080/upload', {
                    method: 'POST',
                    body: formData
                }).then(() => {
                    uploadState.uploadedChunks.push(i);
                    localStorage.setItem(fileId, JSON.stringify(uploadState));
                    updateProgress((uploadState.uploadedChunks.length / totalChunks) * 100);
                })
            );
        }

        await Promise.all(uploadPromises);

        // Xác nhận hoàn thành upload
        const completeResponse = await fetch(`http://localhost:8080/complete/${fileId}`, {
            method: 'POST'
        });
        if (!completeResponse.ok) throw new Error('Complete upload failed');

        // Tải file và verify
        await verifyAndDownload(fileId, file.name, file.size);

    } catch (error) {
        console.error('Upload failed:', error);
        alert(`Upload failed: ${error.message}`);
    } finally {
        localStorage.removeItem(fileId);
    }
}

async function verifyAndDownload(encodedName, originalName, originalSize) {
    try {
        // Kiểm tra metadata
        const metadataResponse = await fetch(`http://localhost:8080/metadata/${encodedName}`);
        if (!metadataResponse.ok) throw new Error('Metadata check failed');

        const metadata = await metadataResponse.json();

        // Verify thông tin file
        if (metadata.original_name !== originalName) {
            throw new Error('File name does not match');
        }

        // Tải file
        const downloadResponse = await fetch(`http://localhost:8080/download/${encodedName}`);
        if (!downloadResponse.ok) throw new Error('Download failed');

        const blob = await downloadResponse.blob();
        if (blob.size !== originalSize) {
            throw new Error('File size does not match');
        }

        // Tạo link download
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = originalName;
        document.body.appendChild(a);
        a.click();
        URL.revokeObjectURL(url);
        document.body.removeChild(a);

        // Verify file hash
        const fileHash = await calculateFileHash(blob);
        if (fileHash !== metadata.file_hash) {
            throw new Error('File hash does not match');
        }
    } catch (error) {
        console.error('Verification failed:', error);
        alert(`Verification failed: ${error.message}`);
    }
}

function updateProgress(percent) {
    const progressBar = document.getElementById('progressBar');
    const status = document.getElementById('status');

    progressBar.style.width = `${percent}%`;
    status.textContent = `${Math.round(percent)}%`;

    // Thêm hiệu ứng khi hoàn thành
    if (percent >= 100) {
        progressBar.style.backgroundColor = '#4CAF50';
        status.textContent = 'Completed!';
    }
}

async function calculateFileHash(file) {
    const buffer = await file.arrayBuffer();
    const hashBuffer = await crypto.subtle.digest('SHA-256', buffer);
    return Array.from(new Uint8Array(hashBuffer))
        .map(b => b.toString(16).padStart(2, '0'))
        .join('');
}
