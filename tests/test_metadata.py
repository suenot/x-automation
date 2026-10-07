import json
import pytest
from metadata_automation import generate
from x_automation.media import read_caption
from x_automation.errors import PublishError


@pytest.mark.parametrize('schema_version',['1.0.0','2.0.0'])
def test_shared_metadata_uses_exact_x_text(tmp_path, schema_version):
    value = generate({'language':'en','title':'Word origins','summary':'Expected caption',
                      'keywords':['word origins']},schema_version=schema_version)
    path = tmp_path/'metadata.json'; path.write_text(json.dumps(value))
    assert read_caption(metadata_file=path) == value['platforms']['x']['text']
    del value['source']
    path.write_text(json.dumps(value))
    with pytest.raises(PublishError): read_caption(metadata_file=path)
