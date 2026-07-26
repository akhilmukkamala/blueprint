# Homebrew formula for the blueprint tap (e.g. akhilmukkamala/homebrew-blueprint).
# Template placeholders are filled by tools/relmanifests from a release
# checksums file — do not hand-edit generated copies; regenerate instead.
# The repo slug is finalized at first publish.
class Blueprint < Formula
  desc "Spec-driven development engine: specs as verifiers, worklogs, autonomy"
  homepage "https://github.com/akhilmukkamala/blueprint"
  version "1.2.3"
  # License finalized at first publish (see LICENSES.md in the repo).

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/akhilmukkamala/blueprint/releases/download/v1.2.3/blueprint-1.2.3-darwin-arm64.tar.gz"
      sha256 "1111111111111111111111111111111111111111111111111111111111111111"
    else
      url "https://github.com/akhilmukkamala/blueprint/releases/download/v1.2.3/blueprint-1.2.3-darwin-amd64.tar.gz"
      sha256 "2222222222222222222222222222222222222222222222222222222222222222"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/akhilmukkamala/blueprint/releases/download/v1.2.3/blueprint-1.2.3-linux-arm64.tar.gz"
      sha256 "4444444444444444444444444444444444444444444444444444444444444444"
    else
      url "https://github.com/akhilmukkamala/blueprint/releases/download/v1.2.3/blueprint-1.2.3-linux-amd64.tar.gz"
      sha256 "3333333333333333333333333333333333333333333333333333333333333333"
    end
  end

  def install
    bin.install "blueprint"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/blueprint --version")
  end
end
