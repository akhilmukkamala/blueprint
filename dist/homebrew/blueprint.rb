# Homebrew formula for the blueprint tap (e.g. akhilmukkamala/homebrew-blueprint).
# Template placeholders are filled by tools/relmanifests from a release
# checksums file — do not hand-edit generated copies; regenerate instead.
class Blueprint < Formula
  desc "Spec-driven development engine: specs as verifiers, worklogs, autonomy"
  homepage "https://github.com/{{REPO}}"
  version "{{VERSION}}"
  # See LICENSES.md in the repo.

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/{{REPO}}/releases/download/v{{VERSION}}/blueprint-{{VERSION}}-darwin-arm64.tar.gz"
      sha256 "{{SHA256_DARWIN_ARM64}}"
    else
      url "https://github.com/{{REPO}}/releases/download/v{{VERSION}}/blueprint-{{VERSION}}-darwin-amd64.tar.gz"
      sha256 "{{SHA256_DARWIN_AMD64}}"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/{{REPO}}/releases/download/v{{VERSION}}/blueprint-{{VERSION}}-linux-arm64.tar.gz"
      sha256 "{{SHA256_LINUX_ARM64}}"
    else
      url "https://github.com/{{REPO}}/releases/download/v{{VERSION}}/blueprint-{{VERSION}}-linux-amd64.tar.gz"
      sha256 "{{SHA256_LINUX_AMD64}}"
    end
  end

  def install
    bin.install "blueprint"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/blueprint --version")
  end
end
