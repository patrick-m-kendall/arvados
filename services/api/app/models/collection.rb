class Collection < ArvadosModel
  include HasUuid
  include KindAndEtag
  include CommonApiTemplate

  api_accessible :user, extend: :common do |t|
    t.add :data_size
    t.add :files
  end

  api_accessible :with_data, extend: :user do |t|
    t.add :manifest_text
  end

  def redundancy_status
    if redundancy_confirmed_as.nil?
      'unconfirmed'
    elsif redundancy_confirmed_as < redundancy
      'degraded'
    else
      if redundancy_confirmed_at.nil?
        'unconfirmed'
      elsif Time.now - redundancy_confirmed_at < 7.days
        'OK'
      else
        'stale'
      end
    end
  end

  def assign_uuid
    if self.manifest_text.nil? and self.uuid.nil?
      super
    elsif self.manifest_text and self.uuid
      self.uuid.gsub! /\+.*/, ''
      if self.uuid == Digest::MD5.hexdigest(self.manifest_text)
        self.uuid.gsub! /$/, '+' + self.manifest_text.length.to_s
        true
      else
        errors.add :uuid, 'does not match checksum of manifest_text'
        false
      end
    elsif self.manifest_text
      errors.add :uuid, 'not supplied (must match checksum of manifest_text)'
      false
    else
      errors.add :manifest_text, 'not supplied'
      false
    end
  end

  def data_size
    inspect_manifest_text if @data_size.nil? or manifest_text_changed?
    @data_size
  end

  def files
    inspect_manifest_text if @files.nil? or manifest_text_changed?
    @files
  end

  def inspect_manifest_text
    if !manifest_text
      @data_size = false
      @files = []
      return
    end

    #normalized_manifest = ""
    #IO.popen(['arv-normalize'], 'w+b') do |io|
    #  io.write manifest_text
    #  io.close_write
    #  while buf = io.read(2**20)
    #    normalized_manifest += buf
    #  end
    #end

    @data_size = 0
    tmp = {}

    manifest_text.split("\n").each do |stream|
      toks = stream.split(" ")

      stream = toks[0].gsub /\\(\\|[0-7]{3})/ do |escape_sequence|
        case $1
        when '\\' '\\'
        else $1.to_i(8).chr
        end
      end

      toks[1..-1].each do |tok|
        if (re = tok.match /^[0-9a-f]{32}/)
          blocksize = nil
          tok.split('+')[1..-1].each do |hint|
            if !blocksize and hint.match /^\d+$/
              blocksize = hint.to_i
            end
            if (re = hint.match /^GS(\d+)$/)
              blocksize = re[1].to_i
            end
          end
          @data_size = false if !blocksize
          @data_size += blocksize if @data_size
        else
          if (re = tok.match /^(\d+):(\d+):(\S+)$/)
            filename = re[3].gsub /\\(\\|[0-7]{3})/ do |escape_sequence|
              case $1
              when '\\' '\\'
              else $1.to_i(8).chr
              end
            end
            fn = stream + '/' + filename
            i = re[2].to_i
            if tmp[fn]
              tmp[fn] += i
            else
              tmp[fn] = i
            end
          end
        end
      end
    end

    @files = []
    tmp.each do |k, v|
      re = k.match(/^(.+)\/(.+)/)
      @files << [re[1], re[2], v]
    end
  end

  def self.uuid_like_pattern
    "________________________________+%"
  end

  def self.normalize_uuid uuid
    hash_part = nil
    size_part = nil
    uuid.split('+').each do |token|
      if token.match /^[0-9a-f]{32,}$/
        raise "uuid #{uuid} has multiple hash parts" if hash_part
        hash_part = token
      elsif token.match /^\d+$/
        raise "uuid #{uuid} has multiple size parts" if size_part
        size_part = token
      end
    end
    raise "uuid #{uuid} has no hash part" if !hash_part
    [hash_part, size_part].compact.join '+'
  end

  def self.for_latest_docker_image(search_term, search_tag=nil, readers=nil)
    readers ||= [Thread.current[:user]]
    base_search = Link.
      readable_by(*readers).
      readable_by(*readers, table_name: "collections").
      joins("JOIN collections ON links.head_uuid = collections.uuid").
      order("links.created_at DESC")

    # If the search term is a Collection locator with an associated
    # Docker image hash link, return that Collection.
    coll_matches = base_search.
      where(link_class: "docker_image_hash", collections: {uuid: search_term})
    if match = coll_matches.first
      return find_by_uuid(match.head_uuid)
    end

    # Find Collections with matching Docker image repository+tag pairs.
    matches = base_search.
      where(link_class: "docker_image_repo+tag",
            name: "#{search_term}:#{search_tag || 'latest'}")

    # If that didn't work, find Collections with matching Docker image hashes.
    if matches.empty?
      matches = base_search.
        where("link_class = ? and name LIKE ?",
              "docker_image_hash", "#{search_term}%")
    end

    # Select the image that was created most recently.  Note that the
    # SQL search order and fallback timestamp values are chosen so
    # that if image timestamps are missing, we use the image with the
    # newest link.
    latest_image_link = nil
    latest_image_timestamp = "1900-01-01T00:00:00Z"
    matches.find_each do |link|
      link_timestamp = link.properties.fetch("image_timestamp",
                                             "1900-01-01T00:00:01Z")
      if link_timestamp > latest_image_timestamp
        latest_image_link = link
        latest_image_timestamp = link_timestamp
      end
    end
    latest_image_link.nil? ? nil : find_by_uuid(latest_image_link.head_uuid)
  end
end
