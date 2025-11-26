# Epherra

Epherra is a secure, ephemeral file-sharing platform designed for privacy and simplicity. It allows users to share files that self-destruct after a set period or a specific number of views. Built with end-to-end encryption (E2EE), Epherra ensures that the server never sees the raw contents of your files.

## Features

*   **Ephemeral Storage:** Files automatically expire after a configurable duration (1 hour to 7 days) or once a view limit is reached.
*   **End-to-End Encryption:** Files are encrypted in the browser *before* uploading. The server only stores encrypted blobs.
*   **Granular Controls:**
    *   Set maximum view counts (e.g., "burn after reading").
    *   Toggle permission for downloading or copying content.
*   **Broad File Support:** Native viewing support for code snippets (syntax highlighting), Jupyter Notebooks, PDFs, images, and videos.
*   **Anti-Tamper UI:** The viewer interface includes measures to discourage scraping, such as disabling context menus and detecting DevTools usage.
*   **No Accounts Required:** Anonymous, frictionless sharing.

## Security Architecture

Epherra employs a **Zero-Knowledge** architecture for file content. It works by:

1.  **Client-Side Encryption:**
    *   When you select a password, the browser generates a random salt and IV (Initialization Vector).
    *   It uses **PBKDF2** (100,000 iterations) to derive a 256-bit encryption key from your password.
    *   The file is encrypted using **AES-GCM** using this derived key.
    *   Only the *encrypted* data is sent to the server.

2.  **Authentication:**
    *   The browser computes a SHA-256 hash of the password and sends it to the server as an authorization token.
    *   The server stores this hash to gatekeep access to the encrypted file but cannot use it to decrypt the file (as decryption requires the PBKDF2 derived key, which depends on the unique per-file salt).

3.  **Decryption:**
    *   When a recipient opens the link, the browser prompts for the password.
    *   The password hash is sent to the server to authorize the download.
    *   Upon success, the encrypted blob is downloaded.
    *   The browser locally derives the decryption key again and decrypts the file in memory for display.

## Tech Stack

*   **Frontend:** Vanilla HTML5, CSS3, and JavaScript.
    *   *Libraries:* `marked` (Markdown), `notebookjs` (`.ipynb` rendering), `ansi_up`.
    *   Uses the **Web Crypto API** for performance and security.
*   **Backend:** Go (Golang) Serverless Functions.
*   **Database:** MongoDB (Metadata) & GridFS (File Storage).

## Local Development

This repository contains the source code for the Epherra platform.

### Prerequisites
*   Go 1.24+
*   MongoDB instance

### Setup

1.  **Clone the repository**
    ```bash
    git clone https://github.com/dhr412/epherra.git
    cd epherra
    ```

2.  **Environment Variables**
    Create a `.env` file in the root directory:
    ```env
    MONGODB_URI=mongodb+srv://user:pass@cluster.mongodb.net/epherra
    CRON_SECRET=your_secret_for_cleanup_jobs
    ```

3.  **Running the Backend**
    The `api/` directory contains Go functions designed for serverless deployment.
    *   *Upload Handler:* `api/upload/upload.go`
    *   *View Handler:* `api/view/view.go`
    *   *Cleanup Handler:* `api/cleanup/cleanup.go`

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
