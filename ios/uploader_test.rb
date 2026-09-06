# frozen_string_literal: true

# Integration test for Uploader against a locally-run file_to_s3.
#
#   cd /Users/grillermo/c/file_to_s3
#   FILES_DIR=/tmp/awh-upload-test AUTH_TOKEN=test-token bin/rackup -s webrick
#
#   cd /Users/grillermo/c/awh
#   AWH_UPLOAD_BASE=http://localhost:33333 AWH_UPLOAD_TOKEN=test-token \
#     ruby ios/uploader_test.rb
#
# Skips itself when no server is listening, so it is safe to run unattended.

require "minitest/autorun"
require "net/http"
require "tempfile"
require "uri"
require_relative "uploader"

class UploaderTest < Minitest::Test
  def setup
    skip "no file_to_s3 at #{Uploader.base_url}" unless server_running?
  end

  def server_running?
    uri = URI(Uploader.base_url)
    Net::HTTP.start(uri.host, uri.port, open_timeout: 1, read_timeout: 2) { |h| h.get("/") }
    true
  rescue StandardError
    false
  end

  def with_file(content)
    Tempfile.create(["awh-upload", ".plist"]) do |f|
      f.write(content)
      f.flush
      yield f.path
    end
  end

  def test_pinned_upload_returns_the_stable_url
    url = with_file("<plist/>") { |p| Uploader.upload(p, name: "awh-test.plist") }

    assert_equal "#{Uploader.base_url}/files/awh-test.plist", url
  end

  def test_pinned_upload_overwrites_and_keeps_the_same_url
    first  = with_file("one") { |p| Uploader.upload(p, name: "awh-test.plist") }
    second = with_file("two") { |p| Uploader.upload(p, name: "awh-test.plist") }

    assert_equal first, second
    assert_equal "two", Net::HTTP.get(URI(second))
  end

  def test_a_missing_file_raises
    assert_raises(SystemExit) { Uploader.upload("/nope/missing.plist", name: "x.plist") }
  end

  def test_a_bad_token_raises
    original = ENV["AWH_UPLOAD_TOKEN"]
    ENV["AWH_UPLOAD_TOKEN"] = "wrong-token"

    assert_raises(SystemExit) do
      with_file("<plist/>") { |p| Uploader.upload(p, name: "awh-test.plist") }
    end
  ensure
    ENV["AWH_UPLOAD_TOKEN"] = original
  end
end
